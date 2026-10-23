package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol/metadata"
	"github.com/segmentio/kafka-go/protocol/produce"
)

type controlledKafkaWriter struct {
	mu       sync.Mutex
	messages []kafka.Message
	writes   int
	closes   int
	writeFn  func(context.Context, ...kafka.Message) error
	closeFn  func() error
}

func (w *controlledKafkaWriter) WriteMessages(ctx context.Context, messages ...kafka.Message) error {
	w.mu.Lock()
	w.writes++
	w.messages = append(w.messages, cloneKafkaMessages(messages)...)
	writeFn := w.writeFn
	w.mu.Unlock()
	if writeFn != nil {
		return writeFn(ctx, messages...)
	}
	return nil
}

func (w *controlledKafkaWriter) Close() error {
	w.mu.Lock()
	w.closes++
	closeFn := w.closeFn
	w.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}

func cloneKafkaMessages(messages []kafka.Message) []kafka.Message {
	cloned := make([]kafka.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].Key = append([]byte(nil), message.Key...)
		cloned[i].Value = append([]byte(nil), message.Value...)
		cloned[i].Headers = append([]kafka.Header(nil), message.Headers...)
		for j := range cloned[i].Headers {
			cloned[i].Headers[j].Value = append([]byte(nil), message.Headers[j].Value...)
		}
	}
	return cloned
}

func testKafkaPublisher(factory kafkaWriterFactory) *KafkaPublisher {
	return &KafkaPublisher{
		config: config.KafkaConfig{
			Brokers:                   []string{"broker:9092"},
			UserBehaviorTopic:         "behavior-topic",
			ActivityEventsTopic:       "activity-topic",
			RecommendationEventsTopic: "recommendation-topic",
		},
		writers:       make(map[string]kafkaMessageWriter),
		writerFactory: factory,
	}
}

func TestNewKafkaWriterUsesBoundedSynchronousConfig(t *testing.T) {
	writer, err := newKafkaWriter(config.KafkaConfig{Brokers: []string{" broker:9092 ", ""}}, "events")
	if err != nil {
		t.Fatalf("newKafkaWriter() error = %v", err)
	}
	if got := writer.Addr.String(); got != "broker:9092" {
		t.Errorf("writer address = %q, want broker:9092", got)
	}
	if writer.Topic != "events" {
		t.Errorf("writer topic = %q, want events", writer.Topic)
	}
	if _, ok := writer.Balancer.(*kafka.Hash); !ok {
		t.Errorf("writer balancer type = %T, want *kafka.Hash", writer.Balancer)
	}
	if writer.RequiredAcks != kafka.RequireAll || writer.Async {
		t.Errorf("writer acknowledgement mode = (%v, async=%t), want RequireAll and synchronous", writer.RequiredAcks, writer.Async)
	}
	if writer.BatchTimeout != kafkaWriterBatchTimeout || writer.MaxAttempts != kafkaWriterMaxAttempts ||
		writer.ReadTimeout != kafkaWriterReadTimeout || writer.WriteTimeout != kafkaWriterWriteTimeout ||
		writer.WriteBackoffMin != kafkaWriterBackoffMin || writer.WriteBackoffMax != kafkaWriterBackoffMax {
		t.Errorf("writer bounded configuration = (%s, %d, %s, %s, %s, %s), want (%s, %d, %s, %s, %s, %s)",
			writer.BatchTimeout, writer.MaxAttempts, writer.ReadTimeout, writer.WriteTimeout, writer.WriteBackoffMin, writer.WriteBackoffMax,
			kafkaWriterBatchTimeout, kafkaWriterMaxAttempts, kafkaWriterReadTimeout, kafkaWriterWriteTimeout, kafkaWriterBackoffMin, kafkaWriterBackoffMax)
	}
}

func TestPublishBatchPreservesEventEnvelopeAndStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	created := make([]string, 0, 2)
	var firstWriter *controlledKafkaWriter
	publisher := testKafkaPublisher(func(_ config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
		created = append(created, topic)
		writer := &controlledKafkaWriter{}
		if topic == "activity-topic" {
			firstWriter = writer
			writer.writeFn = func(context.Context, ...kafka.Message) error {
				cancel()
				return nil
			}
		}
		return writer, nil
	})

	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	first := Envelope{
		ID: "event-1", Type: EventTypeReplyCreated, SchemaVersion: 1,
		AggregateType: "post", AggregateID: "post-12", OccurredAt: when,
		Payload: json.RawMessage(`{"post_id":12,"reply_id":34}`),
	}
	second := Envelope{
		ID: "event-2", Type: EventTypePostViewed, SchemaVersion: 1,
		AggregateType: "post", AggregateID: "post-56", OccurredAt: when.Add(time.Second),
		Payload: json.RawMessage(`{"user_id":7,"post_id":56}`),
	}

	err := publisher.PublishBatch(ctx, []Envelope{first, second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishBatch() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(created, []string{"activity-topic"}) {
		t.Fatalf("created writers for topics %v, want only activity-topic after cancellation", created)
	}
	if firstWriter == nil || firstWriter.writes != 1 || len(firstWriter.messages) != 1 {
		t.Fatalf("first topic writer state = %#v, want one write with one message", firstWriter)
	}
	message := firstWriter.messages[0]
	wantValue, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(message.Key) != KeyForEvent(first) || string(message.Value) != string(wantValue) || !message.Time.Equal(first.OccurredAt) || len(message.Headers) != 0 {
		t.Errorf("published message = %#v, want original event key/value/time and no headers", message)
	}
	var decoded Envelope
	if err := json.Unmarshal(message.Value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != first.ID {
		t.Errorf("published event ID = %q, want %q", decoded.ID, first.ID)
	}
}

func TestPublishBatchReturnsErrorAfterPartialTopicSuccess(t *testing.T) {
	secondTopicErr := errors.New("second topic acknowledgement unknown")
	writers := make(map[string]*controlledKafkaWriter, 2)
	publisher := testKafkaPublisher(func(_ config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
		writer := &controlledKafkaWriter{}
		if topic == "behavior-topic" {
			writer.writeFn = func(context.Context, ...kafka.Message) error { return secondTopicErr }
		}
		writers[topic] = writer
		return writer, nil
	})

	events := []Envelope{
		{ID: "reply-event", Type: EventTypeReplyCreated, SchemaVersion: 1, AggregateType: "post", AggregateID: "post-1", Payload: json.RawMessage(`{"reply_id":1}`)},
		{ID: "view-event", Type: EventTypePostViewed, SchemaVersion: 1, AggregateType: "post", AggregateID: "post-2", Payload: json.RawMessage(`{"user_id":7,"post_id":2}`)},
	}
	err := publisher.PublishBatch(context.Background(), events)
	if !errors.Is(err, secondTopicErr) {
		t.Fatalf("PublishBatch() error = %v, want the second-topic error after a possible partial success", err)
	}
	if writers["activity-topic"] == nil || writers["activity-topic"].writes != 1 ||
		writers["behavior-topic"] == nil || writers["behavior-topic"].writes != 1 {
		t.Fatalf("topic write states = %#v, want one write submitted to each topic", writers)
	}
	var published Envelope
	if err := json.Unmarshal(writers["activity-topic"].messages[0].Value, &published); err != nil {
		t.Fatal(err)
	}
	if published.ID != events[0].ID {
		t.Errorf("successful topic event ID = %q, want preserved ID %q", published.ID, events[0].ID)
	}
}

func TestPublishRawMessagesUsesUnifiedWriterAndClosesOnWriteError(t *testing.T) {
	writeErr := errors.New("write acknowledgement unknown")
	closeErr := errors.New("writer close failed")
	writer := &controlledKafkaWriter{writeFn: func(context.Context, ...kafka.Message) error { return writeErr }}
	writer.closeFn = func() error { return closeErr }
	message := kafka.Message{
		Key: []byte("key"), Value: []byte("value"), Time: time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC),
		Headers: []kafka.Header{{Key: "trace", Value: []byte("trace-1")}},
	}
	var factoryCalls int
	gotConfig := config.KafkaConfig{Brokers: []string{"broker:9092"}}
	err := publishRawMessages(context.Background(), gotConfig, "raw-topic", []kafka.Message{message}, func(cfg config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
		factoryCalls++
		if !reflect.DeepEqual(cfg, gotConfig) || topic != "raw-topic" {
			t.Errorf("writer factory args = (%#v, %q), want config and raw-topic", cfg, topic)
		}
		return writer, nil
	})
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) {
		t.Fatalf("publishRawMessages() error = %v, want write and close errors", err)
	}
	if factoryCalls != 1 || writer.writes != 1 || writer.closes != 1 || !reflect.DeepEqual(writer.messages, []kafka.Message{message}) {
		t.Errorf("temporary writer calls = factory:%d write:%d close:%d messages:%#v", factoryCalls, writer.writes, writer.closes, writer.messages)
	}
}

func TestPublishRawMessagesDoesNotCreateWriterForCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var factoryCalls int
	err := publishRawMessages(ctx, config.KafkaConfig{Brokers: []string{"broker:9092"}}, "raw-topic", []kafka.Message{{Value: []byte("value")}}, func(config.KafkaConfig, string) (kafkaMessageWriter, error) {
		factoryCalls++
		return &controlledKafkaWriter{}, nil
	})
	if !errors.Is(err, context.Canceled) || factoryCalls != 0 {
		t.Fatalf("publishRawMessages() = (%v, factory calls %d), want context.Canceled and no Writer", err, factoryCalls)
	}
}

func TestPublishRawMessagesCancellationStillClosesTemporaryWriter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &controlledKafkaWriter{writeFn: func(context.Context, ...kafka.Message) error {
		cancel()
		return context.Canceled
	}}
	err := publishRawMessages(ctx, config.KafkaConfig{Brokers: []string{"broker:9092"}}, "dlq-topic", []kafka.Message{{Value: []byte("dlq")}}, func(config.KafkaConfig, string) (kafkaMessageWriter, error) {
		return writer, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publishRawMessages() error = %v, want context.Canceled", err)
	}
	if writer.writes != 1 || writer.closes != 1 {
		t.Errorf("temporary writer calls = write:%d close:%d, want one of each", writer.writes, writer.closes)
	}
}

func TestKafkaPublisherRejectsWritesAfterCloseAndClosesWritersInParallel(t *testing.T) {
	closeErr := errors.New("close failed")
	closeStarted := make(chan string, 2)
	releaseClose := make(chan struct{})
	var factoryCalls int
	publisher := testKafkaPublisher(func(_ config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
		factoryCalls++
		return &controlledKafkaWriter{closeFn: func() error {
			closeStarted <- topic
			<-releaseClose
			return closeErr
		}}, nil
	})

	if err := publisher.PublishRaw(context.Background(), "topic-a", kafka.Message{Value: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishRaw(context.Background(), "topic-b", kafka.Message{Value: []byte("b")}); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() { results <- publisher.Close() }()
	go func() { results <- publisher.Close() }()

	startedTopics := map[string]bool{}
	for range 2 {
		select {
		case topic := <-closeStarted:
			startedTopics[topic] = true
		case <-time.After(3 * time.Second):
			close(releaseClose)
			t.Fatalf("only %d topic writers began closing; want both closed concurrently", len(startedTopics))
		}
	}
	close(releaseClose)

	firstErr := <-results
	secondErr := <-results
	if !errors.Is(firstErr, closeErr) || !errors.Is(secondErr, closeErr) || firstErr.Error() != secondErr.Error() {
		t.Fatalf("concurrent Close() errors = (%v, %v), want the same joined close result", firstErr, secondErr)
	}
	if len(startedTopics) != 2 || factoryCalls != 2 {
		t.Fatalf("closed topics = %v; writer factory calls = %d", startedTopics, factoryCalls)
	}
	if err := publisher.PublishRaw(context.Background(), "topic-c", kafka.Message{Value: []byte("c")}); !errors.Is(err, errKafkaPublisherClosed) {
		t.Errorf("PublishRaw() after Close error = %v, want publisher-closed error", err)
	}
	if factoryCalls != 2 {
		t.Errorf("writer factory called %d times after Close, want no new writer", factoryCalls)
	}
	if err := publisher.Close(); err == nil || err.Error() != firstErr.Error() {
		t.Errorf("repeated Close() error = %v, want %v", err, firstErr)
	}
}

func TestKafkaPublisherPublishCloseRaceDoesNotRegisterWriterAfterClosingStarts(t *testing.T) {
	writeStarted := make(chan struct{})
	finishWrite := make(chan struct{})
	closeStarted := make(chan struct{})
	finishClose := make(chan struct{})
	var factoryCalls int
	writer := &controlledKafkaWriter{
		writeFn: func(context.Context, ...kafka.Message) error {
			close(writeStarted)
			<-finishWrite
			return nil
		},
		closeFn: func() error {
			close(closeStarted)
			<-finishClose
			return nil
		},
	}
	publisher := testKafkaPublisher(func(config.KafkaConfig, string) (kafkaMessageWriter, error) {
		factoryCalls++
		return writer, nil
	})

	publishResult := make(chan error, 1)
	go func() {
		publishResult <- publisher.PublishRaw(context.Background(), "topic-a", kafka.Message{Value: []byte("in flight")})
	}()
	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishRaw did not reach the controlled write")
	}

	closeResult := make(chan error, 1)
	go func() { closeResult <- publisher.Close() }()
	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not reach the controlled writer")
	}
	if err := publisher.PublishRaw(context.Background(), "topic-b", kafka.Message{Value: []byte("late")}); !errors.Is(err, errKafkaPublisherClosed) {
		t.Errorf("concurrent PublishRaw error = %v, want publisher-closed error", err)
	}
	if factoryCalls != 1 {
		t.Errorf("writer factory calls during close = %d, want 1", factoryCalls)
	}
	close(finishClose)
	if err := <-closeResult; err != nil {
		t.Errorf("Close() error = %v", err)
	}
	close(finishWrite)
	if err := <-publishResult; err != nil {
		t.Errorf("PublishRaw that acquired its Writer before Close returned %v, want it may finish successfully", err)
	}
}

type kafkaTimeoutError struct{}

func (kafkaTimeoutError) Error() string   { return "simulated broker timeout" }
func (kafkaTimeoutError) Timeout() bool   { return true }
func (kafkaTimeoutError) Temporary() bool { return true }

type stalledKafkaTransport struct {
	produceStarted chan struct{}
	mu             sync.Mutex
	produceCalls   int
}

func (transport *stalledKafkaTransport) RoundTrip(ctx context.Context, _ net.Addr, request kafka.Request) (kafka.Response, error) {
	switch request := request.(type) {
	case *metadata.Request:
		topics := make([]metadata.ResponseTopic, 0, len(request.TopicNames))
		for _, topic := range request.TopicNames {
			topics = append(topics, metadata.ResponseTopic{
				Name: topic,
				Partitions: []metadata.ResponsePartition{{
					PartitionIndex: 0,
					LeaderID:       1,
				}},
			})
		}
		return &metadata.Response{Topics: topics}, nil
	case *produce.Request:
		transport.mu.Lock()
		transport.produceCalls++
		transport.mu.Unlock()
		transport.produceStarted <- struct{}{}
		<-ctx.Done()
		return nil, kafkaTimeoutError{}
	default:
		return nil, fmt.Errorf("unexpected Kafka request type %T", request)
	}
}

func TestKafkaWriterCancellationClosesAfterBoundedProduceRetries(t *testing.T) {
	transport := &stalledKafkaTransport{produceStarted: make(chan struct{}, 4)}
	publisher, err := NewKafkaPublisher(config.KafkaConfig{Brokers: []string{"broker:9092"}})
	if err != nil {
		t.Fatal(err)
	}
	publisher.writerFactory = func(kafkaConfig config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
		writer, err := newKafkaWriter(kafkaConfig, topic)
		if err != nil {
			return nil, err
		}
		writer.Transport = transport
		return writer, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- publisher.PublishRaw(ctx, "stalled-topic", kafka.Message{Key: []byte("key"), Value: []byte("value")})
	}()
	select {
	case <-transport.produceStarted:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("writer did not submit a Produce request to the stalled broker")
	}
	cancel()
	select {
	case err := <-publishResult:
		if err == nil {
			t.Fatal("PublishRaw unexpectedly reported success without a Produce acknowledgement")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PublishRaw did not return after its context was canceled")
	}

	closeStartedAt := time.Now()
	closeResult := make(chan error, 1)
	go func() { closeResult <- publisher.Close() }()
	select {
	case <-transport.produceStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not make its configured second bounded Produce attempt")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("Close did not finish after bounded Produce retries")
	}
	closeElapsed := time.Since(closeStartedAt)
	transport.mu.Lock()
	produceCalls := transport.produceCalls
	transport.mu.Unlock()
	if produceCalls != kafkaWriterMaxAttempts {
		t.Errorf("Produce attempts = %d, want configured MaxAttempts=%d", produceCalls, kafkaWriterMaxAttempts)
	}
	t.Logf("publisher Close after canceled stalled Produce completed in %s", closeElapsed)
	if closeElapsed >= 7*time.Second {
		t.Errorf("publisher Close elapsed %s, want less than 7s for one stalled topic/partition batch", closeElapsed)
	}
}

func TestPublishRawMessagesCancellationWaitsForTemporaryWriterClose(t *testing.T) {
	transport := &stalledKafkaTransport{produceStarted: make(chan struct{}, 4)}
	kafkaConfig := config.KafkaConfig{Brokers: []string{"broker:9092"}}
	ctx, cancel := context.WithCancel(context.Background())
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- publishRawMessages(ctx, kafkaConfig, "temporary-stalled-topic", []kafka.Message{{Value: []byte("value")}}, func(cfg config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
			writer, err := newKafkaWriter(cfg, topic)
			if err != nil {
				return nil, err
			}
			writer.Transport = transport
			return writer, nil
		})
	}()
	select {
	case <-transport.produceStarted:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("temporary writer did not submit a Produce request to the stalled broker")
	}
	canceledAt := time.Now()
	cancel()
	select {
	case err := <-publishResult:
		if err == nil {
			t.Fatal("temporary writer reported success without a Produce acknowledgement")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("temporary writer did not close after bounded Produce retries")
	}
	elapsed := time.Since(canceledAt)
	transport.mu.Lock()
	produceCalls := transport.produceCalls
	transport.mu.Unlock()
	if produceCalls != kafkaWriterMaxAttempts {
		t.Errorf("temporary writer Produce attempts = %d, want MaxAttempts=%d", produceCalls, kafkaWriterMaxAttempts)
	}
	t.Logf("temporary Writer close after canceled stalled Produce completed in %s", elapsed)
	if elapsed >= 7*time.Second {
		t.Errorf("temporary Writer close elapsed %s, want less than 7s for one stalled topic/partition batch", elapsed)
	}
}

func TestKafkaWriterUnavailableBrokerUsesConfiguredAttemptLimit(t *testing.T) {
	transport := &stalledKafkaTransport{produceStarted: make(chan struct{}, 4)}
	transportRoundTrip := func(ctx context.Context, addr net.Addr, request kafka.Request) (kafka.Response, error) {
		if _, ok := request.(*produce.Request); ok {
			transport.mu.Lock()
			transport.produceCalls++
			transport.mu.Unlock()
			return nil, kafkaTimeoutError{}
		}
		return transport.RoundTrip(ctx, addr, request)
	}
	writer, err := newKafkaWriter(config.KafkaConfig{Brokers: []string{"broker:9092"}}, "unavailable-topic")
	if err != nil {
		t.Fatal(err)
	}
	writer.Transport = kafkaRoundTripAdapter{roundTrip: transportRoundTrip}
	writeErr := writer.WriteMessages(context.Background(), kafka.Message{Value: []byte("value")})
	if writeErr == nil {
		t.Fatal("WriteMessages unexpectedly succeeded against the simulated unavailable broker")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	transport.mu.Lock()
	produceCalls := transport.produceCalls
	transport.mu.Unlock()
	if produceCalls != kafkaWriterMaxAttempts {
		t.Errorf("Produce attempts = %d, want configured MaxAttempts=%d", produceCalls, kafkaWriterMaxAttempts)
	}
}

type kafkaRoundTripAdapter struct {
	roundTrip func(context.Context, net.Addr, kafka.Request) (kafka.Response, error)
}

func (adapter kafkaRoundTripAdapter) RoundTrip(ctx context.Context, addr net.Addr, request kafka.Request) (kafka.Response, error) {
	return adapter.roundTrip(ctx, addr, request)
}

var _ net.Error = kafkaTimeoutError{}

func TestKafkaWriterErrorDoesNotImplyMessageWasNotAccepted(t *testing.T) {
	// This test records the contract at the call site: any unconfirmed write is
	// returned as an error so callers retain their retry or recovery state.
	unknownOutcome := errors.New("connection lost after request transmission")
	writer := &controlledKafkaWriter{writeFn: func(context.Context, ...kafka.Message) error { return unknownOutcome }}
	publisher := testKafkaPublisher(func(config.KafkaConfig, string) (kafkaMessageWriter, error) { return writer, nil })
	err := publisher.PublishRaw(context.Background(), "topic", kafka.Message{Value: []byte("possibly accepted")})
	if !errors.Is(err, unknownOutcome) {
		t.Fatalf("PublishRaw() error = %v, want the original unknown-outcome error", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "not published") {
		t.Fatalf("PublishRaw error incorrectly claims non-delivery: %v", err)
	}
}
