package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	appmetrics "Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type postEmbeddingTestReader struct {
	messages    []kafka.Message
	index       int
	fetchCalls  int
	commitCalls int
	committed   []kafka.Message
	commitErr   error
	stopErr     error
	publisher   *fakeRawKafkaMessagePublisher
}

func (r *postEmbeddingTestReader) FetchMessage(context.Context) (kafka.Message, error) {
	r.fetchCalls++
	if r.index >= len(r.messages) {
		if r.stopErr != nil {
			return kafka.Message{}, r.stopErr
		}
		return kafka.Message{}, errors.New("test reader stopped")
	}
	message := r.messages[r.index]
	r.index++
	return message, nil
}

func (r *postEmbeddingTestReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.commitCalls++
	r.committed = append(r.committed, messages...)
	return r.commitErr
}

func (*postEmbeddingTestReader) Close() error { return nil }

type postEmbeddingTestEmbedder struct {
	calls   int
	err     error
	errors  []error
	results []embeddings.EmbedResult
}

type postEmbeddingTestStore struct {
	post              models.Post
	postErr           error
	postErrors        []error
	getPostCalls      int
	onGetPost         func(int)
	embedding         models.PostEmbedding
	embeddingErr      error
	embeddingErrors   []error
	getEmbeddingCalls int
	writeOutcome      postEmbeddingWriteOutcome
	writeOutcomes     []postEmbeddingWriteOutcome
	writeErr          error
	writeErrors       []error
	writeCalls        int
	upserted          []models.PostEmbedding
}

func (s *postEmbeddingTestStore) GetPost(context.Context, uint) (models.Post, error) {
	s.getPostCalls++
	if s.onGetPost != nil {
		s.onGetPost(s.getPostCalls)
	}
	return s.post, popPostEmbeddingTestError(&s.postErrors, s.postErr)
}

func (s *postEmbeddingTestStore) GetEmbedding(context.Context, uint) (models.PostEmbedding, error) {
	s.getEmbeddingCalls++
	if len(s.upserted) > 0 {
		return s.upserted[len(s.upserted)-1], nil
	}
	return s.embedding, popPostEmbeddingTestError(&s.embeddingErrors, s.embeddingErr)
}

func (s *postEmbeddingTestStore) CommitEmbeddingIfCurrent(_ context.Context, embedding models.PostEmbedding, _ time.Time) (postEmbeddingWriteOutcome, error) {
	s.writeCalls++
	if writeErr := popPostEmbeddingTestError(&s.writeErrors, s.writeErr); writeErr != nil {
		return postEmbeddingWriteCommitted, writeErr
	}
	outcome := s.writeOutcome
	if len(s.writeOutcomes) > 0 {
		outcome = s.writeOutcomes[0]
		s.writeOutcomes = s.writeOutcomes[1:]
	}
	if outcome == postEmbeddingWriteCommitted {
		s.upserted = append(s.upserted, embedding)
	}
	return outcome, nil
}

func popPostEmbeddingTestError(queue *[]error, fallback error) error {
	if len(*queue) == 0 {
		return fallback
	}
	err := (*queue)[0]
	*queue = (*queue)[1:]
	return err
}

func newPostEmbeddingTestStore() *postEmbeddingTestStore {
	return &postEmbeddingTestStore{
		post:         models.Post{Model: gorm.Model{ID: 42}, Content: "Body", Visibility: "public"},
		embeddingErr: gorm.ErrRecordNotFound,
	}
}

func (e *postEmbeddingTestEmbedder) Embed(context.Context, []string) (embeddings.EmbedResult, error) {
	e.calls++
	index := e.calls - 1
	if index < len(e.errors) && e.errors[index] != nil {
		return embeddings.EmbedResult{}, e.errors[index]
	}
	if e.err != nil {
		return embeddings.EmbedResult{}, e.err
	}
	if index < len(e.results) {
		return e.results[index], nil
	}
	return embeddings.EmbedResult{Vectors: [][]float32{{1, 2}}, Model: "test-model"}, nil
}

func postEmbeddingTestMessage(t *testing.T, postID uint) kafka.Message {
	t.Helper()
	event, err := eventing.NewPostEmbeddingRequestedEnvelope(uuid.NewString(), postID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.post.embedding.v1", Partition: 1, Offset: 11, Value: value}
}

func postEmbeddingRecoveryConfig() config.KafkaConfig {
	return config.KafkaConfig{ConsumerDLQTopic: "goexchange.consumer.dlq.v1"}
}

func consumePostEmbeddingTestMessageWithPolicy(
	t *testing.T,
	message kafka.Message,
	store postEmbeddingStore,
	embedder embeddings.Embedder,
	commitErr error,
	policy kafkaRetryPolicy,
) (*postEmbeddingTestReader, error) {
	t.Helper()
	reader := &postEmbeddingTestReader{
		messages: []kafka.Message{message}, commitErr: commitErr, stopErr: errors.New("test reader stopped"),
		publisher: &fakeRawKafkaMessagePublisher{},
	}
	err := consumePostEmbeddingMessagesWithPolicy(
		context.Background(), reader, reader.publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), policy,
	)
	return reader, err
}

func TestPostEmbeddingPermanentDecodeFailureGoesToDLQAndCommits(t *testing.T) {
	message := kafka.Message{Topic: "goexchange.post.embedding.v1", Partition: 3, Offset: 77, Key: []byte("post-key"), Value: []byte("{")}
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{}
	messageDLQBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCodeDecodeEnvelope)
	reader, err := consumePostEmbeddingTestMessageWithPolicy(t, message, store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, reader.stopErr) || reader.commitCalls != 1 || embedder.calls != 0 || store.getPostCalls != 0 || store.getEmbeddingCalls != 0 || store.writeCalls != 0 {
		t.Fatalf("err=%v commits=%d provider=%d post_reads=%d embedding_reads=%d writes=%d", err, reader.commitCalls, embedder.calls, store.getPostCalls, store.getEmbeddingCalls, store.writeCalls)
	}
	if len(reader.publisher.messages) != 1 || len(reader.publisher.topics) != 1 || reader.publisher.topics[0] != postEmbeddingRecoveryConfig().ConsumerDLQTopic {
		t.Fatalf("DLQ topics=%v messages=%d", reader.publisher.topics, len(reader.publisher.messages))
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(reader.publisher.messages[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Consumer != kafkaConsumerPostEmbedding || payload.SourceTopic != message.Topic || payload.SourcePartition != message.Partition || payload.SourceOffset != message.Offset ||
		payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeDecodeEnvelope || string(payload.SourceValue) != string(message.Value) {
		t.Fatalf("DLQ payload=%+v", payload)
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCodeDecodeEnvelope, messageDLQBefore)
}

func TestPostEmbeddingDLQFailureDoesNotCommit(t *testing.T) {
	store := newPostEmbeddingTestStore()
	dlqFailureBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCodeDLQPublish)
	reader := &postEmbeddingTestReader{
		messages: []kafka.Message{{Topic: "post-embedding", Offset: 1, Value: []byte("{")}},
		stopErr:  errors.New("should not fetch again"), publisher: &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")},
	}
	err := consumePostEmbeddingMessagesWithPolicy(context.Background(), reader, reader.publisher, &postEmbeddingTestEmbedder{}, store, "v1", postEmbeddingRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish || reader.commitCalls != 0 || store.getPostCalls != 0 {
		t.Fatalf("err=%v class=%q code=%q commits=%d post_reads=%d", err, kafkaFailureClassOf(err), kafkaFailureCode(err), reader.commitCalls, store.getPostCalls)
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCodeDLQPublish, dlqFailureBefore)
}

func TestPostEmbeddingConsumerDoesNotCommitWhenProcessingFails(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.postErr = errors.New("article read failed")
	reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, &postEmbeddingTestEmbedder{}, nil, kafkaRetryPolicy{MaxAttempts: 1})
	if err == nil || reader.commitCalls != 0 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
}

func TestDecodePostEmbeddingMessageClassifiesPermanentFailures(t *testing.T) {
	now := time.Now().UTC()
	base := eventing.Envelope{
		ID: uuid.NewString(), Type: eventing.EventTypePostEmbeddingRequested,
		SchemaVersion: 1, AggregateType: "post", AggregateID: "42",
		OccurredAt: now, Payload: []byte("{\"post_id\":42}"),
	}
	tests := []struct {
		name string
		msg  kafka.Message
		edit func(*eventing.Envelope)
		code string
	}{
		{name: "malformed envelope", msg: kafka.Message{Value: []byte("{")}, code: kafkaFailureCodeDecodeEnvelope},
		{name: "invalid uuid", edit: func(event *eventing.Envelope) { event.ID = "bad" }, code: kafkaFailureCodeInvalidPayload},
		{name: "wrong type", edit: func(event *eventing.Envelope) { event.Type = eventing.EventTypePostViewed }, code: kafkaFailureCodeUnsupportedEvent},
		{name: "unsupported schema", edit: func(event *eventing.Envelope) { event.SchemaVersion = 2 }, code: kafkaFailureCodeUnsupportedSchema},
		{name: "wrong aggregate type", edit: func(event *eventing.Envelope) { event.AggregateType = "user" }, code: kafkaFailureCodeInvalidPayload},
		{name: "missing occurred at", edit: func(event *eventing.Envelope) { event.OccurredAt = time.Time{} }, code: kafkaFailureCodeInvalidPayload},
		{name: "malformed payload", edit: func(event *eventing.Envelope) { event.Payload = []byte("\"bad\"") }, code: kafkaFailureCodeDecodePayload},
		{name: "zero post", edit: func(event *eventing.Envelope) { event.AggregateID = "0"; event.Payload = []byte("{\"post_id\":0}") }, code: kafkaFailureCodeInvalidPayload},
		{name: "aggregate mismatch", edit: func(event *eventing.Envelope) { event.AggregateID = "41" }, code: kafkaFailureCodeInvalidPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := test.msg
			if test.edit != nil {
				event := base
				test.edit(&event)
				var err error
				message, err = postEmbeddingMessageForEnvelope(event)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := decodePostEmbeddingMessage(message)
			if kafkaFailureClassOf(err) != kafkaFailurePermanent || kafkaFailureCode(err) != test.code {
				t.Fatalf("class=%q code=%q err=%v want permanent/%s", kafkaFailureClassOf(err), kafkaFailureCode(err), err, test.code)
			}
		})
	}
}

func postEmbeddingMessageForEnvelope(event eventing.Envelope) (kafka.Message, error) {
	value, err := json.Marshal(event)
	if err != nil {
		return kafka.Message{}, err
	}
	return kafka.Message{Topic: "goexchange.post.embedding.v1", Partition: 1, Offset: 11, Value: value}, nil
}

func consumePostEmbeddingTestMessage(t *testing.T, store postEmbeddingStore, embedder embeddings.Embedder, commitErr error) (*postEmbeddingTestReader, error) {
	t.Helper()
	return consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, commitErr, kafkaRetryPolicy{MaxAttempts: 1})
}

func postEmbeddingRecoveryMetric(t *testing.T, outcome, code string) float64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	appmetrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	prefix := fmt.Sprintf("go_exchange_kafka_consumer_recovery_total{code=%q,consumer=%q,outcome=%q} ", code, kafkaConsumerPostEmbedding, outcome)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse recovery metric %q: %v", line, err)
		}
		return value
	}
	return 0
}

func postEmbeddingDomainMetric(t *testing.T, metricName, labelName, labelValue string) float64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	appmetrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	prefix := fmt.Sprintf("%s{%s=%q} ", metricName, labelName, labelValue)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse metric %q: %v", line, err)
		}
		return value
	}
	return 0
}

func assertPostEmbeddingRecoveryIncrement(t *testing.T, outcome, code string, before float64) {
	t.Helper()
	if got := postEmbeddingRecoveryMetric(t, outcome, code); got != before+1 {
		t.Fatalf("recovery metric %s/%s=%v want %v", outcome, code, got, before+1)
	}
}

func TestPostEmbeddingConsumerGeneratesMissingProjection(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{}
	appliedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageApplied, kafkaRecoveryCodeNone)
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
	if embedder.calls != 1 || store.writeCalls != 1 || len(store.upserted) != 1 {
		t.Fatalf("provider_calls=%d write_calls=%d upserts=%d", embedder.calls, store.writeCalls, len(store.upserted))
	}
	got := store.upserted[0]
	if got.PostID != 42 || got.Version != "v1" || got.Model != "test-model" || got.Dimensions != 2 ||
		got.ContentHash != embeddings.PostEmbeddingContentHash("Body") {
		t.Fatalf("embedding=%#v", got)
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageApplied, kafkaRecoveryCodeNone, appliedBefore)
}

func TestPostEmbeddingConsumerSkipsCurrentProjection(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.embeddingErr = nil
	store.embedding = models.PostEmbedding{
		PostID: 42, Version: "v1", ContentHash: embeddings.PostEmbeddingContentHash("Body"),
	}
	embedder := &postEmbeddingTestEmbedder{}
	noopBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
		t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone, noopBefore)
}

func TestPostEmbeddingConsumerRegeneratesStaleVersionAndContent(t *testing.T) {
	tests := []struct {
		name    string
		version string
		hash    string
	}{
		{name: "stale version", version: "old", hash: embeddings.PostEmbeddingContentHash("Body")},
		{name: "stale content", version: "v1", hash: "old-hash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			store.embeddingErr = nil
			store.embedding = models.PostEmbedding{PostID: 42, Version: test.version, ContentHash: test.hash}
			embedder := &postEmbeddingTestEmbedder{}
			reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || store.writeCalls != 1 || len(store.upserted) != 1 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
		})
	}
}

func TestPostEmbeddingConsumerCommitsMissingPosts(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.postErr = gorm.ErrRecordNotFound
		embedder := &postEmbeddingTestEmbedder{}
		noopBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
			t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
		}
		assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone, noopBefore)
	})
}

func TestPostEmbeddingConsumerDoesNotCommitStaleResult(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.writeOutcome = postEmbeddingWriteStaleContent
	embedder := &postEmbeddingTestEmbedder{}
	redeliveryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCodeSourceChanged)
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if !errors.Is(err, errPostEmbeddingSourceChanged) || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeSourceChanged || reader.commitCalls != 0 || embedder.calls != 1 || store.writeCalls != 1 || len(store.upserted) != 0 || len(reader.publisher.messages) != 0 {
		t.Fatalf("err=%v commits=%d provider=%d writes=%d upserts=%d", err, reader.commitCalls, embedder.calls, store.writeCalls, len(store.upserted))
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCodeSourceChanged, redeliveryBefore)
}

func TestPostEmbeddingConsumerRedeliversStaleResultAndCommitsLatestContent(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.writeOutcome = postEmbeddingWriteStaleContent
	embedder := &postEmbeddingTestEmbedder{}
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if !errors.Is(err, errPostEmbeddingSourceChanged) || reader.commitCalls != 0 {
		t.Fatalf("first err=%v commits=%d", err, reader.commitCalls)
	}

	store.post.Content = "Body v2"
	store.writeOutcome = postEmbeddingWriteCommitted
	reader, err = consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 2 || store.writeCalls != 2 || len(store.upserted) != 1 {
		t.Fatalf("second err=%v commits=%d provider=%d writes=%d upserts=%d", err, reader.commitCalls, embedder.calls, store.writeCalls, len(store.upserted))
	}
	if got, want := store.upserted[0].ContentHash, embeddings.PostEmbeddingContentHash("Body v2"); got != want {
		t.Fatalf("content_hash=%q want=%q", got, want)
	}
}

func TestPostEmbeddingConsumerCommitsWhenPostDisappearsAfterProviderCall(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.writeOutcome = postEmbeddingWritePostMissing
	embedder := &postEmbeddingTestEmbedder{}
	noopBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || store.writeCalls != 1 || len(store.upserted) != 0 {
		t.Fatalf("err=%v commits=%d provider=%d writes=%d upserts=%d", err, reader.commitCalls, embedder.calls, store.writeCalls, len(store.upserted))
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone, noopBefore)
}

func TestPostEmbeddingConsumerProviderRetryFailuresDoNotCommit(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "429", err: &embeddings.ProviderHTTPError{StatusCode: 429}},
		{name: "503", err: &embeddings.ProviderHTTPError{StatusCode: 503}},
		{name: "401", err: &embeddings.ProviderHTTPError{StatusCode: 401}},
		{name: "403", err: &embeddings.ProviderHTTPError{StatusCode: 403}},
		{name: "404", err: &embeddings.ProviderHTTPError{StatusCode: 404}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			embedder := &postEmbeddingTestEmbedder{err: test.err}
			reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 0 || embedder.calls != 1 || len(store.upserted) != 0 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
		})
	}
}

func TestPostEmbeddingConsumerPermanentProviderErrorsGoToDLQAndCommit(t *testing.T) {
	for _, status := range []int{400, 413, 422} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: status}}
			reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 5})
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 0 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
			if len(reader.publisher.messages) != 1 {
				t.Fatalf("DLQ messages=%d want=1", len(reader.publisher.messages))
			}
			var payload consumerDLQPayload
			if err := json.Unmarshal(reader.publisher.messages[0].Value, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ErrorCode != kafkaFailureCodeProviderPermanent {
				t.Fatalf("DLQ code=%q want=%q", payload.ErrorCode, kafkaFailureCodeProviderPermanent)
			}
		})
	}
}

func TestPostEmbeddingConsumerDBReadAndUpsertFailuresDoNotCommit(t *testing.T) {
	t.Run("post read", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.postErr = errors.New("post read failed")
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 0 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
	t.Run("projection read", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.embeddingErr = errors.New("projection read failed")
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 0 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
	t.Run("upsert", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.writeErr = errors.New("conditional write failed")
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 1 || store.writeCalls != 1 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
}

func TestPostEmbeddingConsumerCommitFailureRedeliverySkipsProvider(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{}
	commitErr := errors.New("commit failed")
	redeliveryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCodeKafkaCommit)
	retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeKafkaCommit)
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, commitErr)
	if !errors.Is(err, commitErr) || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("first err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCodeKafkaCommit, redeliveryBefore)
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeKafkaCommit); got != retryBefore {
		t.Fatalf("Kafka commit emitted retry_attempt: before=%v after=%v", retryBefore, got)
	}

	reader, err = consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("redelivery err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
}

func TestPostEmbeddingRetryableProviderFailureRetriesInProcess(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{
		errors: []error{
			&embeddings.ProviderHTTPError{StatusCode: 503},
			&embeddings.ProviderHTTPError{StatusCode: 503},
			nil,
		},
	}
	retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable)
	reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 5})
	if !errors.Is(err, reader.stopErr) || embedder.calls != 3 || store.getPostCalls != 1 || store.getEmbeddingCalls != 1 || store.writeCalls != 1 || reader.commitCalls != 1 || len(reader.publisher.messages) != 0 {
		t.Fatalf("err=%v provider=%d post_reads=%d embedding_reads=%d writes=%d commits=%d DLQ=%d", err, embedder.calls, store.getPostCalls, store.getEmbeddingCalls, store.writeCalls, reader.commitCalls, len(reader.publisher.messages))
	}
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable); got != retryBefore+2 {
		t.Fatalf("provider retry_attempt metric=%v want=%v", got, retryBefore+2)
	}
}

func TestPostEmbeddingProviderRetryExhaustionDoesNotCommitOrDLQ(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: 503}}
	exhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable)
	reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 3})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeProviderRetryable || embedder.calls != 3 || reader.commitCalls != 0 || len(reader.publisher.messages) != 0 {
		t.Fatalf("class=%q code=%q err=%v provider=%d commits=%d DLQ=%d", kafkaFailureClassOf(err), kafkaFailureCode(err), err, embedder.calls, reader.commitCalls, len(reader.publisher.messages))
	}
	assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable, exhaustedBefore)
}

func TestPostEmbeddingInvalidProviderContractsGoToDLQ(t *testing.T) {
	tests := []struct {
		name   string
		result embeddings.EmbedResult
	}{
		{name: "zero vectors", result: embeddings.EmbedResult{Model: "model"}},
		{name: "multiple vectors", result: embeddings.EmbedResult{Vectors: [][]float32{{1}, {2}}, Model: "model"}},
		{name: "empty vector", result: embeddings.EmbedResult{Vectors: [][]float32{{}}, Model: "model"}},
		{name: "NaN", result: embeddings.EmbedResult{Vectors: [][]float32{{float32(math.NaN())}}, Model: "model"}},
		{name: "positive infinity", result: embeddings.EmbedResult{Vectors: [][]float32{{float32(math.Inf(1))}}, Model: "model"}},
		{name: "negative infinity", result: embeddings.EmbedResult{Vectors: [][]float32{{float32(math.Inf(-1))}}, Model: "model"}},
		{name: "empty model", result: embeddings.EmbedResult{Vectors: [][]float32{{1}}, Model: "  "}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			embedder := &postEmbeddingTestEmbedder{results: []embeddings.EmbedResult{test.result}}
			reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 5})
			if !errors.Is(err, reader.stopErr) || reader.commitCalls != 1 || embedder.calls != 1 || store.writeCalls != 0 || len(reader.publisher.messages) != 1 {
				t.Fatalf("err=%v commits=%d provider=%d writes=%d DLQ=%d", err, reader.commitCalls, embedder.calls, store.writeCalls, len(reader.publisher.messages))
			}
			var payload consumerDLQPayload
			if err := json.Unmarshal(reader.publisher.messages[0].Value, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeProviderContractInvalid {
				t.Fatalf("DLQ failure=%s/%s", payload.ErrorClass, payload.ErrorCode)
			}
		})
	}
}

func TestPostEmbeddingRealProviderContractFailuresGoToDLQWithoutRetry(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed_json", body: "{"},
		{name: "empty_vector", body: `{"model":"served-model","data":[{"index":0,"embedding":[]}]}`},
		{name: "empty_model", body: `{"model":"  ","data":[{"index":0,"embedding":[1,2]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requestCount++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			embedder, err := embeddings.NewOpenAICompatibleEmbedder(config.EmbeddingConfig{
				BaseURL: server.URL, APIKey: "test-key", Model: "requested-model", TimeoutSeconds: 2,
			})
			if err != nil {
				t.Fatal(err)
			}

			message := postEmbeddingTestMessage(t, 42)
			store := newPostEmbeddingTestStore()
			dlqBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCodeProviderContractInvalid)
			retryableBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable)
			contractRetryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderContractInvalid)
			contractExhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderContractInvalid)
			failureBefore := postEmbeddingDomainMetric(t, "go_exchange_post_embedding_failures_total", "stage", "provider")
			nonRetryableBefore := postEmbeddingDomainMetric(t, "go_exchange_post_embedding_events_total", "result", "provider_non_retryable")
			reader, consumeErr := consumePostEmbeddingTestMessageWithPolicy(
				t, message, store, embedder, nil,
				kafkaRetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
			)
			if !errors.Is(consumeErr, reader.stopErr) || requestCount != 1 || reader.commitCalls != 1 || len(reader.committed) != 1 || reader.committed[0].Offset != message.Offset || store.writeCalls != 0 || len(store.upserted) != 0 || len(reader.publisher.messages) != 1 {
				t.Fatalf("err=%v requests=%d commits=%d committed=%d writes=%d DLQ=%d", consumeErr, requestCount, reader.commitCalls, len(reader.committed), store.writeCalls, len(reader.publisher.messages))
			}
			var payload consumerDLQPayload
			if err := json.Unmarshal(reader.publisher.messages[0].Value, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Consumer != kafkaConsumerPostEmbedding || payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeProviderContractInvalid {
				t.Fatalf("DLQ consumer=%q failure=%s/%s", payload.Consumer, payload.ErrorClass, payload.ErrorCode)
			}
			assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCodeProviderContractInvalid, dlqBefore)
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable); got != retryableBefore {
				t.Fatalf("provider_retryable retry_attempt=%v want unchanged %v", got, retryableBefore)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderContractInvalid); got != contractRetryBefore {
				t.Fatalf("provider_contract_invalid retry_attempt=%v want unchanged %v", got, contractRetryBefore)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderContractInvalid); got != contractExhaustedBefore {
				t.Fatalf("provider_contract_invalid retry_exhausted=%v want unchanged %v", got, contractExhaustedBefore)
			}
			if got := postEmbeddingDomainMetric(t, "go_exchange_post_embedding_failures_total", "stage", "provider"); got != failureBefore+1 {
				t.Fatalf("provider failure metric=%v want %v", got, failureBefore+1)
			}
			if got := postEmbeddingDomainMetric(t, "go_exchange_post_embedding_events_total", "result", "provider_non_retryable"); got != nonRetryableBefore+1 {
				t.Fatalf("provider_non_retryable metric=%v want %v", got, nonRetryableBefore+1)
			}
		})
	}
}

func TestPostEmbeddingOpenAICompatibleProviderHTTPStatusRecovery(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		attempts         int
		wantRequests     int
		wantClass        kafkaFailureClass
		wantCode         string
		wantCommits      int
		wantDLQ          int
		wantRetries      float64
		wantRetryExhaust float64
		wantStopErr      bool
	}{
		{name: "503 retries", status: http.StatusServiceUnavailable, attempts: 3, wantRequests: 3, wantClass: kafkaFailureRetryable, wantCode: kafkaFailureCodeProviderRetryable, wantRetries: 2, wantRetryExhaust: 1},
		{name: "422 permanent", status: http.StatusUnprocessableEntity, attempts: 5, wantRequests: 1, wantClass: kafkaFailurePermanent, wantCode: kafkaFailureCodeProviderPermanent, wantCommits: 1, wantDLQ: 1, wantStopErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requestCount++
				http.Error(w, "provider failure", test.status)
			}))
			defer server.Close()
			embedder, err := embeddings.NewOpenAICompatibleEmbedder(config.EmbeddingConfig{
				BaseURL: server.URL, APIKey: "test-key", Model: "requested-model", TimeoutSeconds: 2,
			})
			if err != nil {
				t.Fatal(err)
			}

			store := newPostEmbeddingTestStore()
			retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, test.wantCode)
			exhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, test.wantCode)
			dlqBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageDLQ, test.wantCode)
			reader, consumeErr := consumePostEmbeddingTestMessageWithPolicy(
				t, postEmbeddingTestMessage(t, 42), store, embedder, nil,
				kafkaRetryPolicy{MaxAttempts: test.attempts},
			)
			if requestCount != test.wantRequests || reader.commitCalls != test.wantCommits || store.writeCalls != 0 || len(reader.publisher.messages) != test.wantDLQ {
				t.Fatalf("err=%v requests=%d commits=%d writes=%d DLQ=%d", consumeErr, requestCount, reader.commitCalls, store.writeCalls, len(reader.publisher.messages))
			}
			if test.wantStopErr {
				if !errors.Is(consumeErr, reader.stopErr) {
					t.Fatalf("err=%v want reader stop error", consumeErr)
				}
			} else if kafkaFailureClassOf(consumeErr) != test.wantClass || kafkaFailureCode(consumeErr) != test.wantCode {
				t.Fatalf("class=%q code=%q err=%v want %s/%s", kafkaFailureClassOf(consumeErr), kafkaFailureCode(consumeErr), consumeErr, test.wantClass, test.wantCode)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, test.wantCode); got != retryBefore+test.wantRetries {
				t.Fatalf("retry_attempt=%v want %v", got, retryBefore+test.wantRetries)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, test.wantCode); got != exhaustedBefore+test.wantRetryExhaust {
				t.Fatalf("retry_exhausted=%v want %v", got, exhaustedBefore+test.wantRetryExhaust)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeMessageDLQ, test.wantCode); got != dlqBefore+float64(test.wantDLQ) {
				t.Fatalf("message_dlq=%v want %v", got, dlqBefore+float64(test.wantDLQ))
			}
			if test.wantDLQ == 1 {
				var payload consumerDLQPayload
				if err := json.Unmarshal(reader.publisher.messages[0].Value, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Consumer != kafkaConsumerPostEmbedding || payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeProviderPermanent {
					t.Fatalf("DLQ consumer=%q failure=%s/%s", payload.Consumer, payload.ErrorClass, payload.ErrorCode)
				}
			}
		})
	}
}

func TestPostEmbeddingDBPreflightRetriesEachReadStage(t *testing.T) {
	t.Run("GetPost", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.postErrors = []error{errors.New("temporary post read 1"), errors.New("temporary post read 2"), nil}
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 3})
		if !errors.Is(err, reader.stopErr) || store.getPostCalls != 3 || store.getEmbeddingCalls != 1 || embedder.calls != 1 || reader.commitCalls != 1 || len(reader.publisher.messages) != 0 {
			t.Fatalf("err=%v post_reads=%d embedding_reads=%d provider=%d commits=%d DLQ=%d", err, store.getPostCalls, store.getEmbeddingCalls, embedder.calls, reader.commitCalls, len(reader.publisher.messages))
		}
	})
	t.Run("GetEmbedding", func(t *testing.T) {
		store := newPostEmbeddingTestStore()
		store.embeddingErrors = []error{errors.New("temporary embedding read 1"), errors.New("temporary embedding read 2"), gorm.ErrRecordNotFound}
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 3})
		if !errors.Is(err, reader.stopErr) || store.getPostCalls != 1 || store.getEmbeddingCalls != 3 || embedder.calls != 1 || reader.commitCalls != 1 || len(reader.publisher.messages) != 0 {
			t.Fatalf("err=%v post_reads=%d embedding_reads=%d provider=%d commits=%d DLQ=%d", err, store.getPostCalls, store.getEmbeddingCalls, embedder.calls, reader.commitCalls, len(reader.publisher.messages))
		}
	})
}

func TestPostEmbeddingCommitRetryDoesNotReinvokeProvider(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.writeErrors = []error{errors.New("temporary commit 1"), errors.New("temporary commit 2"), nil}
	embedder := &postEmbeddingTestEmbedder{}
	retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeDatabaseTransaction)
	reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 3})
	if !errors.Is(err, reader.stopErr) || embedder.calls != 1 || store.writeCalls != 3 || reader.commitCalls != 1 || len(store.upserted) != 1 {
		t.Fatalf("err=%v provider=%d writes=%d commits=%d upserts=%d", err, embedder.calls, store.writeCalls, reader.commitCalls, len(store.upserted))
	}
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeDatabaseTransaction); got != retryBefore+2 {
		t.Fatalf("DB retry_attempt metric=%v want=%v", got, retryBefore+2)
	}
}

func TestPostEmbeddingDBRetryExhaustionLeavesMessageUncommitted(t *testing.T) {
	tests := []struct {
		name          string
		prepare       func(*postEmbeddingTestStore)
		wantProvider  int
		wantPostReads int
	}{
		{name: "GetPost", prepare: func(store *postEmbeddingTestStore) { store.postErr = errors.New("database unavailable") }, wantProvider: 0, wantPostReads: 3},
		{name: "commit", prepare: func(store *postEmbeddingTestStore) { store.writeErr = errors.New("database unavailable") }, wantProvider: 1, wantPostReads: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			test.prepare(store)
			embedder := &postEmbeddingTestEmbedder{}
			exhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeDatabaseTransaction)
			reader, err := consumePostEmbeddingTestMessageWithPolicy(t, postEmbeddingTestMessage(t, 42), store, embedder, nil, kafkaRetryPolicy{MaxAttempts: 3})
			if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction || store.getPostCalls != test.wantPostReads || embedder.calls != test.wantProvider || reader.commitCalls != 0 || len(reader.publisher.messages) != 0 {
				t.Fatalf("class=%q code=%q err=%v post_reads=%d provider=%d commits=%d DLQ=%d", kafkaFailureClassOf(err), kafkaFailureCode(err), err, store.getPostCalls, embedder.calls, reader.commitCalls, len(reader.publisher.messages))
			}
			assertPostEmbeddingRecoveryIncrement(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeDatabaseTransaction, exhaustedBefore)
		})
	}
}

func TestPostEmbeddingStartupValidationRunsBeforeFetch(t *testing.T) {
	validReader := &postEmbeddingTestReader{stopErr: io.EOF}
	validStore := newPostEmbeddingTestStore()
	validEmbedder := &postEmbeddingTestEmbedder{}
	validConfig := postEmbeddingRecoveryConfig()
	tests := []struct {
		name    string
		ctx     context.Context
		reader  postEmbeddingMessageReader
		embed   embeddings.Embedder
		store   postEmbeddingStore
		version string
		config  config.KafkaConfig
	}{
		{name: "nil context", ctx: nil, reader: validReader, embed: validEmbedder, store: validStore, version: "v1", config: validConfig},
		{name: "nil reader", ctx: context.Background(), reader: nil, embed: validEmbedder, store: validStore, version: "v1", config: validConfig},
		{name: "nil embedder", ctx: context.Background(), reader: validReader, embed: nil, store: validStore, version: "v1", config: validConfig},
		{name: "nil store", ctx: context.Background(), reader: validReader, embed: validEmbedder, store: nil, version: "v1", config: validConfig},
		{name: "empty version", ctx: context.Background(), reader: validReader, embed: validEmbedder, store: validStore, version: "  ", config: validConfig},
		{name: "empty DLQ topic", ctx: context.Background(), reader: validReader, embed: validEmbedder, store: validStore, version: "v1", config: config.KafkaConfig{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := consumePostEmbeddingMessagesWithPolicy(test.ctx, test.reader, nil, test.embed, test.store, test.version, test.config, kafkaRetryPolicy{MaxAttempts: 1})
			if err == nil {
				t.Fatal("expected startup validation error")
			}
		})
	}
	if validReader.fetchCalls != 0 {
		t.Fatalf("FetchMessage called %d times before validation completed", validReader.fetchCalls)
	}
}

func TestPostEmbeddingProviderCancellationStopsRetryAndCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelTimer := time.AfterFunc(25*time.Millisecond, cancel)
	defer cancelTimer.Stop()
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: 503}}
	reader := &postEmbeddingTestReader{
		messages: []kafka.Message{postEmbeddingTestMessage(t, 42)}, stopErr: errors.New("should not fetch again"),
		publisher: &fakeRawKafkaMessagePublisher{},
	}
	err := consumePostEmbeddingMessagesWithPolicy(ctx, reader, reader.publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5, InitialBackoff: time.Hour, MaxBackoff: time.Hour})
	if !errors.Is(err, context.Canceled) || embedder.calls != 1 || reader.commitCalls != 0 || len(reader.publisher.messages) != 0 {
		t.Fatalf("err=%v provider=%d commits=%d DLQ=%d", err, embedder.calls, reader.commitCalls, len(reader.publisher.messages))
	}
}

func TestPostEmbeddingDBCancellationStopsRetryAndCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelTimer := time.AfterFunc(25*time.Millisecond, cancel)
	defer cancelTimer.Stop()
	store := newPostEmbeddingTestStore()
	store.postErr = errors.New("temporary post read")
	reader := &postEmbeddingTestReader{
		messages: []kafka.Message{postEmbeddingTestMessage(t, 42)}, stopErr: errors.New("should not fetch again"),
		publisher: &fakeRawKafkaMessagePublisher{},
	}
	err := consumePostEmbeddingMessagesWithPolicy(ctx, reader, reader.publisher, &postEmbeddingTestEmbedder{}, store, "v1", postEmbeddingRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5, InitialBackoff: time.Hour, MaxBackoff: time.Hour})
	if !errors.Is(err, context.Canceled) || store.getPostCalls != 1 || reader.commitCalls != 0 || len(reader.publisher.messages) != 0 {
		t.Fatalf("err=%v post_reads=%d commits=%d DLQ=%d", err, store.getPostCalls, reader.commitCalls, len(reader.publisher.messages))
	}
}
