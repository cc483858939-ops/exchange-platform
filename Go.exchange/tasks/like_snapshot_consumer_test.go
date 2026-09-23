package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

var errFakeLikeSnapshotFetchDone = errors.New("fake reader exhausted")

type fakeLikeSnapshotReader struct {
	messages   []kafka.Message
	fetchErr   error
	commitErr  error
	fetchIndex int
	commits    []kafka.Message
	closeCalls int
}

func (r *fakeLikeSnapshotReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if err := ctx.Err(); err != nil {
		return kafka.Message{}, err
	}
	if r.fetchIndex < len(r.messages) {
		message := r.messages[r.fetchIndex]
		r.fetchIndex++
		return message, nil
	}
	if r.fetchErr != nil {
		return kafka.Message{}, r.fetchErr
	}
	return kafka.Message{}, errFakeLikeSnapshotFetchDone
}

func (r *fakeLikeSnapshotReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.commits = append(r.commits, messages...)
	return r.commitErr
}

func (r *fakeLikeSnapshotReader) Close() error {
	r.closeCalls++
	return nil
}

type fakeRawKafkaMessagePublisher struct {
	topics   []string
	messages []kafka.Message
	err      error
}

func (p *fakeRawKafkaMessagePublisher) PublishRaw(_ context.Context, topic string, messages ...kafka.Message) error {
	p.topics = append(p.topics, topic)
	p.messages = append(p.messages, messages...)
	return p.err
}

func likeSnapshotTestConfig() config.KafkaConfig {
	return config.KafkaConfig{
		LikeSnapshotGroupID: "like-snapshot-test-group",
		ConsumerDLQTopic:    "goexchange.consumer.dlq.v1",
	}
}

func mustLikeSnapshotMessage(t *testing.T, offset int64, postID uint, count, version int64) kafka.Message {
	t.Helper()
	event, err := eventing.NewLikeSnapshotEnvelope(postID, count, version)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.post.like.snapshot.v1", Partition: 2, Offset: offset, Key: []byte(fmt.Sprintf("post:%d", postID)), Value: value}
}

func malformedLikeSnapshotMessage(offset int64) kafka.Message {
	return kafka.Message{Topic: "goexchange.post.like.snapshot.v1", Partition: 2, Offset: offset, Key: []byte("poison"), Value: []byte("{"), Time: time.Now().UTC()}
}

func fastKafkaRetryPolicy(attempts int) kafkaRetryPolicy {
	return kafkaRetryPolicy{MaxAttempts: attempts, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
}

func TestLikeSnapshotConsumerPermanentMessageDoesNotBlockFollowingMessage(t *testing.T) {
	reader := &fakeLikeSnapshotReader{
		messages: []kafka.Message{malformedLikeSnapshotMessage(100), mustLikeSnapshotMessage(t, 101, 55, 4, 8)},
		fetchErr: errFakeLikeSnapshotFetchDone,
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(3), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		applyCalls++
		return likeSnapshotApplied, nil
	})
	if !errors.Is(err, errFakeLikeSnapshotFetchDone) {
		t.Fatalf("consume error=%v want fake end-of-stream", err)
	}
	if len(publisher.messages) != 1 || publisher.topics[0] != "goexchange.consumer.dlq.v1" {
		t.Fatalf("DLQ publications=%d topics=%v", len(publisher.messages), publisher.topics)
	}
	if len(reader.commits) != 2 || reader.commits[0].Offset != 100 || reader.commits[1].Offset != 101 {
		t.Fatalf("committed offsets=%v want [100 101]", messageOffsets(reader.commits))
	}
	if applyCalls != 1 {
		t.Fatalf("apply calls=%d want only the valid following message", applyCalls)
	}
	if reader.closeCalls != 1 {
		t.Fatalf("reader close calls=%d want 1", reader.closeCalls)
	}
	var dlqPayload consumerDLQPayload
	if err := json.Unmarshal(publisher.messages[0].Value, &dlqPayload); err != nil {
		t.Fatal(err)
	}
	if dlqPayload.SourceOffset != 100 || dlqPayload.ErrorCode != kafkaFailureCodeDecodeEnvelope || dlqPayload.Attempts != 1 {
		t.Fatalf("poison message DLQ metadata=%+v", dlqPayload)
	}
}

func TestLikeSnapshotMalformedEnvelopeIsDLQedWithoutApplyRetries(t *testing.T) {
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err := processLikeSnapshotMessageWithApply(context.Background(), nil, publisher, likeSnapshotTestConfig(), malformedLikeSnapshotMessage(20), fastKafkaRetryPolicy(5), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		applyCalls++
		return likeSnapshotNoop, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if applyCalls != 0 || len(publisher.messages) != 1 {
		t.Fatalf("apply calls=%d DLQ publishes=%d want 0/1", applyCalls, len(publisher.messages))
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(publisher.messages[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeDecodeEnvelope {
		t.Fatalf("DLQ classification=%s/%s", payload.ErrorClass, payload.ErrorCode)
	}
}

func TestLikeSnapshotDLQFailurePreventsSourceCommit(t *testing.T) {
	publishFailure := errors.New("Kafka DLQ unavailable")
	reader := &fakeLikeSnapshotReader{messages: []kafka.Message{malformedLikeSnapshotMessage(100)}, fetchErr: errFakeLikeSnapshotFetchDone}
	publisher := &fakeRawKafkaMessagePublisher{err: publishFailure}
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(3), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		return likeSnapshotApplied, nil
	})
	if !errors.Is(err, publishFailure) || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("consume error=%v code=%q", err, kafkaFailureCode(err))
	}
	if len(reader.commits) != 0 {
		t.Fatalf("commits=%v want none", messageOffsets(reader.commits))
	}
	if len(publisher.messages) != 1 {
		t.Fatalf("DLQ publish calls=%d want 1", len(publisher.messages))
	}
}

func TestLikeSnapshotRetryableDatabaseFailureRetriesWithoutDLQ(t *testing.T) {
	reader := &fakeLikeSnapshotReader{messages: []kafka.Message{mustLikeSnapshotMessage(t, 7, 12, 3, 5)}, fetchErr: errFakeLikeSnapshotFetchDone}
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(5), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		applyCalls++
		if applyCalls < 3 {
			return likeSnapshotNoop, retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("temporary database error"))
		}
		return likeSnapshotApplied, nil
	})
	if !errors.Is(err, errFakeLikeSnapshotFetchDone) {
		t.Fatalf("consume error=%v want fake end-of-stream", err)
	}
	if applyCalls != 3 || len(reader.commits) != 1 || reader.commits[0].Offset != 7 || len(publisher.messages) != 0 {
		t.Fatalf("apply calls=%d commits=%v DLQ publishes=%d", applyCalls, messageOffsets(reader.commits), len(publisher.messages))
	}
}

func TestLikeSnapshotRetryExhaustionDoesNotCommitOrDLQ(t *testing.T) {
	reader := &fakeLikeSnapshotReader{messages: []kafka.Message{mustLikeSnapshotMessage(t, 8, 12, 3, 5)}}
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	applyFailure := errors.New("database is unavailable")
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(3), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		applyCalls++
		return likeSnapshotNoop, retryableKafkaError(kafkaFailureCodeDatabaseUnavailable, applyFailure)
	})
	if !errors.Is(err, applyFailure) || kafkaFailureCode(err) != kafkaFailureCodeDatabaseUnavailable {
		t.Fatalf("consume error=%v code=%q", err, kafkaFailureCode(err))
	}
	if applyCalls != 3 || len(reader.commits) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("apply calls=%d commits=%v DLQ publishes=%d", applyCalls, messageOffsets(reader.commits), len(publisher.messages))
	}
}

func TestLikeSnapshotCommitFailureDoesNotPublishDLQ(t *testing.T) {
	commitFailure := errors.New("Kafka commit failed")
	reader := &fakeLikeSnapshotReader{messages: []kafka.Message{mustLikeSnapshotMessage(t, 9, 12, 3, 5)}, commitErr: commitFailure}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(2), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		return likeSnapshotApplied, nil
	})
	if !errors.Is(err, commitFailure) || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit {
		t.Fatalf("consume error=%v code=%q", err, kafkaFailureCode(err))
	}
	if len(reader.commits) != 1 || len(publisher.messages) != 0 {
		t.Fatalf("commit attempts=%d DLQ publishes=%d", len(reader.commits), len(publisher.messages))
	}
}

func TestLikeSnapshotNoopIsCommitReady(t *testing.T) {
	reader := &fakeLikeSnapshotReader{messages: []kafka.Message{mustLikeSnapshotMessage(t, 10, 12, 3, 5)}, fetchErr: errFakeLikeSnapshotFetchDone}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeLikeSnapshotMessagesWithApply(context.Background(), reader, publisher, nil, likeSnapshotTestConfig(), fastKafkaRetryPolicy(2), func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
		return likeSnapshotNoop, nil
	})
	if !errors.Is(err, errFakeLikeSnapshotFetchDone) || len(reader.commits) != 1 || reader.commits[0].Offset != 10 || len(publisher.messages) != 0 {
		t.Fatalf("err=%v commits=%v DLQ publishes=%d", err, messageOffsets(reader.commits), len(publisher.messages))
	}
}

func TestDecodeLikeSnapshotMessageClassifiesPermanentFailures(t *testing.T) {
	wrongType, _ := json.Marshal(eventing.Envelope{ID: "evt", Type: eventing.EventTypePostLiked, SchemaVersion: 1})
	wrongSchema, _ := json.Marshal(eventing.Envelope{ID: "evt", Type: eventing.EventTypePostLikeSnapshot, SchemaVersion: 2})
	badPayload, _ := json.Marshal(eventing.Envelope{ID: "evt", Type: eventing.EventTypePostLikeSnapshot, SchemaVersion: 1, Payload: json.RawMessage(`"not-an-object"`)})
	invalidPayload, _ := json.Marshal(eventing.Envelope{ID: "evt", Type: eventing.EventTypePostLikeSnapshot, SchemaVersion: 1, Payload: json.RawMessage(`{"post_id":4,"like_count":1,"version":0}`)})
	tests := []struct {
		name string
		data []byte
		code string
	}{
		{name: "malformed envelope", data: []byte("{"), code: kafkaFailureCodeDecodeEnvelope},
		{name: "unsupported event", data: wrongType, code: kafkaFailureCodeUnsupportedEvent},
		{name: "unsupported schema", data: wrongSchema, code: kafkaFailureCodeUnsupportedSchema},
		{name: "malformed payload", data: badPayload, code: kafkaFailureCodeDecodePayload},
		{name: "invalid payload", data: invalidPayload, code: kafkaFailureCodeInvalidPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := decodeLikeSnapshotMessage(kafka.Message{Value: test.data})
			if kafkaFailureClassOf(err) != kafkaFailurePermanent || kafkaFailureCode(err) != test.code {
				t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
			}
		})
	}
}

func messageOffsets(messages []kafka.Message) []int64 {
	offsets := make([]int64, len(messages))
	for index, message := range messages {
		offsets[index] = message.Offset
	}
	return offsets
}
