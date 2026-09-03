package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type postEmbeddingTestReader struct {
	messages    []kafka.Message
	index       int
	commitCalls int
	committed   []kafka.Message
	commitErr   error
	stopErr     error
}

func (r *postEmbeddingTestReader) FetchMessage(context.Context) (kafka.Message, error) {
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
	calls int
	err   error
}

type postEmbeddingTestStore struct {
	post         models.Post
	postErr      error
	embedding    models.PostEmbedding
	embeddingErr error
	upsertErr    error
	upserted     []models.PostEmbedding
}

func (s *postEmbeddingTestStore) GetPost(context.Context, uint) (models.Post, error) {
	return s.post, s.postErr
}

func (s *postEmbeddingTestStore) GetEmbedding(context.Context, uint) (models.PostEmbedding, error) {
	if len(s.upserted) > 0 {
		return s.upserted[len(s.upserted)-1], nil
	}
	if s.embeddingErr != nil {
		return models.PostEmbedding{}, s.embeddingErr
	}
	return s.embedding, nil
}

func (s *postEmbeddingTestStore) UpsertEmbedding(_ context.Context, embedding models.PostEmbedding) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = append(s.upserted, embedding)
	return nil
}

func newPostEmbeddingTestStore() *postEmbeddingTestStore {
	return &postEmbeddingTestStore{
		post:         models.Post{Model: gorm.Model{ID: 42}, Content: "Body", Visibility: "public"},
		embeddingErr: gorm.ErrRecordNotFound,
	}
}

func (e *postEmbeddingTestEmbedder) Embed(context.Context, []string) (embeddings.EmbedResult, error) {
	e.calls++
	if e.err != nil {
		return embeddings.EmbedResult{}, e.err
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
	return kafka.Message{Value: value}
}

func TestConsumePostEmbeddingMessagesCommitsPoisonMessages(t *testing.T) {
	reader := &postEmbeddingTestReader{
		messages: []kafka.Message{
			{Value: []byte("{")},
			{Value: []byte("{\"id\":\"" + uuid.NewString() + "\",\"type\":\"post.embedding.requested\",\"payload\":{\"post_id\":0}}")},
		},
		stopErr: errors.New("done"),
	}
	if err := consumePostEmbeddingMessages(context.Background(), reader, &postEmbeddingTestEmbedder{}, newPostEmbeddingTestStore(), "v1"); !errors.Is(err, reader.stopErr) {
		t.Fatalf("err=%v", err)
	}
	if reader.commitCalls != len(reader.messages) {
		t.Fatalf("commits=%d want=%d", reader.commitCalls, len(reader.messages))
	}
}

func TestConsumePostEmbeddingMessagesDoesNotCommitWhenProcessingFails(t *testing.T) {
	reader := &postEmbeddingTestReader{messages: []kafka.Message{postEmbeddingTestMessage(t, 42)}, stopErr: errors.New("should not fetch again")}
	store := newPostEmbeddingTestStore()
	store.postErr = errors.New("article read failed")
	err := consumePostEmbeddingMessages(context.Background(), reader, &postEmbeddingTestEmbedder{}, store, "v1")
	if err == nil || reader.commitCalls != 0 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
}

func TestDecodePostEmbeddingRequestRejectsWrongTypeAndMalformedPayload(t *testing.T) {
	wrongType := eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypePostViewed, Payload: []byte("{\"post_id\":1}")}
	wrongTypeRaw, _ := json.Marshal(wrongType)
	for _, raw := range [][]byte{[]byte("not-json"), wrongTypeRaw, []byte("{\"id\":\"" + uuid.NewString() + "\",\"type\":\"post.embedding.requested\",\"payload\":\"bad\"}")} {
		if _, err := decodePostEmbeddingRequest(raw); err == nil {
			t.Fatalf("raw=%s expected decode error", raw)
		}
	}
}

func TestDecodePostEmbeddingRequestStrictContract(t *testing.T) {
	now := time.Now().UTC()
	base := eventing.Envelope{
		ID: uuid.NewString(), Type: eventing.EventTypePostEmbeddingRequested,
		SchemaVersion: 1, AggregateType: "post", AggregateID: "42",
		OccurredAt: now, Payload: []byte("{\"post_id\":42}"),
	}
	tests := []struct {
		name string
		edit func(*eventing.Envelope)
	}{
		{name: "invalid uuid", edit: func(event *eventing.Envelope) { event.ID = "bad" }},
		{name: "wrong type", edit: func(event *eventing.Envelope) { event.Type = eventing.EventTypePostViewed }},
		{name: "unsupported schema", edit: func(event *eventing.Envelope) { event.SchemaVersion = 2 }},
		{name: "wrong aggregate type", edit: func(event *eventing.Envelope) { event.AggregateType = "user" }},
		{name: "missing occurred at", edit: func(event *eventing.Envelope) { event.OccurredAt = time.Time{} }},
		{name: "aggregate mismatch", edit: func(event *eventing.Envelope) { event.AggregateID = "41" }},
		{name: "zero post", edit: func(event *eventing.Envelope) {
			event.AggregateID = "0"
			event.Payload = []byte("{\"post_id\":0}")
		}},
		{name: "malformed payload", edit: func(event *eventing.Envelope) { event.Payload = []byte("\"bad\"") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base
			test.edit(&event)
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodePostEmbeddingRequest(raw); err == nil {
				t.Fatal("expected strict contract error")
			}
		})
	}
}

func consumePostEmbeddingTestMessage(t *testing.T, store postEmbeddingStore, embedder embeddings.Embedder, commitErr error) (*postEmbeddingTestReader, error) {
	t.Helper()
	stopErr := errors.New("test reader stopped")
	reader := &postEmbeddingTestReader{
		messages:  []kafka.Message{postEmbeddingTestMessage(t, 42)},
		commitErr: commitErr,
		stopErr:   stopErr,
	}
	return reader, consumePostEmbeddingMessages(context.Background(), reader, embedder, store, "v1")
}

func TestPostEmbeddingConsumerGeneratesMissingProjection(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{}
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
	if embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("provider_calls=%d upserts=%d", embedder.calls, len(store.upserted))
	}
	got := store.upserted[0]
	if got.PostID != 42 || got.Version != "v1" || got.Model != "test-model" || got.Dimensions != 2 ||
		got.ContentHash != embeddings.PostEmbeddingContentHash("Body") {
		t.Fatalf("embedding=%#v", got)
	}
}

func TestPostEmbeddingConsumerSkipsCurrentProjection(t *testing.T) {
	store := newPostEmbeddingTestStore()
	store.embeddingErr = nil
	store.embedding = models.PostEmbedding{
		PostID: 42, Version: "v1", ContentHash: embeddings.PostEmbeddingContentHash("Body"),
	}
	embedder := &postEmbeddingTestEmbedder{}
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
		t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
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
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
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
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
			t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
		}
	})
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

func TestPostEmbeddingConsumerTerminalProviderErrorsCommit(t *testing.T) {
	for _, status := range []int{400, 413, 422} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			store := newPostEmbeddingTestStore()
			embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: status}}
			reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 0 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
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
		store.upsertErr = errors.New("upsert failed")
		embedder := &postEmbeddingTestEmbedder{}
		reader, err := consumePostEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 1 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
}

func TestPostEmbeddingConsumerCommitFailureRedeliverySkipsProvider(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{}
	commitErr := errors.New("commit failed")
	reader, err := consumePostEmbeddingTestMessage(t, store, embedder, commitErr)
	if !errors.Is(err, commitErr) || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("first err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}

	reader, err = consumePostEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("redelivery err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
}
