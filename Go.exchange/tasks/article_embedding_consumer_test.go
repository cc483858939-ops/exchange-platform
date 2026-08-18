package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type articleEmbeddingTestReader struct {
	messages    []kafka.Message
	index       int
	commitCalls int
	committed   []kafka.Message
	commitErr   error
	stopErr     error
}

func (r *articleEmbeddingTestReader) FetchMessage(context.Context) (kafka.Message, error) {
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

func (r *articleEmbeddingTestReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.commitCalls++
	r.committed = append(r.committed, messages...)
	return r.commitErr
}

func (*articleEmbeddingTestReader) Close() error { return nil }

type articleEmbeddingTestEmbedder struct {
	calls int
	err   error
}

type articleEmbeddingTestStore struct {
	article      models.Article
	articleErr   error
	embedding    models.ArticleEmbedding
	embeddingErr error
	upsertErr    error
	upserted     []models.ArticleEmbedding
}

func (s *articleEmbeddingTestStore) GetArticle(context.Context, uint) (models.Article, error) {
	return s.article, s.articleErr
}

func (s *articleEmbeddingTestStore) GetEmbedding(context.Context, uint) (models.ArticleEmbedding, error) {
	if len(s.upserted) > 0 {
		return s.upserted[len(s.upserted)-1], nil
	}
	if s.embeddingErr != nil {
		return models.ArticleEmbedding{}, s.embeddingErr
	}
	return s.embedding, nil
}

func (s *articleEmbeddingTestStore) UpsertEmbedding(_ context.Context, embedding models.ArticleEmbedding) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = append(s.upserted, embedding)
	return nil
}

func newArticleEmbeddingTestStore() *articleEmbeddingTestStore {
	now := time.Now().UTC()
	return &articleEmbeddingTestStore{
		article: models.Article{
			Model: gorm.Model{ID: 42}, Title: "Title", Content: "Body",
			PublicationState: consts.ArticlePublicationStatePublished, PublishedAt: &now,
		},
		embeddingErr: gorm.ErrRecordNotFound,
	}
}

func (e *articleEmbeddingTestEmbedder) Embed(context.Context, []string) (embeddings.EmbedResult, error) {
	e.calls++
	if e.err != nil {
		return embeddings.EmbedResult{}, e.err
	}
	return embeddings.EmbedResult{Vectors: [][]float32{{1, 2}}, Model: "test-model"}, nil
}

func articleEmbeddingTestMessage(t *testing.T, articleID uint) kafka.Message {
	t.Helper()
	event, err := eventing.NewArticleEmbeddingRequestedEnvelope(uuid.NewString(), articleID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: value}
}

func TestConsumeArticleEmbeddingMessagesCommitsPoisonMessages(t *testing.T) {
	reader := &articleEmbeddingTestReader{
		messages: []kafka.Message{
			{Value: []byte("{")},
			{Value: []byte("{\"id\":\"" + uuid.NewString() + "\",\"type\":\"article.embedding.requested\",\"payload\":{\"article_id\":0}}")},
		},
		stopErr: errors.New("done"),
	}
	if err := consumeArticleEmbeddingMessages(context.Background(), reader, &articleEmbeddingTestEmbedder{}, newArticleEmbeddingTestStore(), "v1"); !errors.Is(err, reader.stopErr) {
		t.Fatalf("err=%v", err)
	}
	if reader.commitCalls != len(reader.messages) {
		t.Fatalf("commits=%d want=%d", reader.commitCalls, len(reader.messages))
	}
}

func TestConsumeArticleEmbeddingMessagesDoesNotCommitWhenProcessingFails(t *testing.T) {
	reader := &articleEmbeddingTestReader{messages: []kafka.Message{articleEmbeddingTestMessage(t, 42)}, stopErr: errors.New("should not fetch again")}
	store := newArticleEmbeddingTestStore()
	store.articleErr = errors.New("article read failed")
	err := consumeArticleEmbeddingMessages(context.Background(), reader, &articleEmbeddingTestEmbedder{}, store, "v1")
	if err == nil || reader.commitCalls != 0 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
}

func TestDecodeArticleEmbeddingRequestRejectsWrongTypeAndMalformedPayload(t *testing.T) {
	wrongType := eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypeArticleViewed, Payload: []byte("{\"article_id\":1}")}
	wrongTypeRaw, _ := json.Marshal(wrongType)
	for _, raw := range [][]byte{[]byte("not-json"), wrongTypeRaw, []byte("{\"id\":\"" + uuid.NewString() + "\",\"type\":\"article.embedding.requested\",\"payload\":\"bad\"}")} {
		if _, err := decodeArticleEmbeddingRequest(raw); err == nil {
			t.Fatalf("raw=%s expected decode error", raw)
		}
	}
}

func TestDecodeArticleEmbeddingRequestStrictContract(t *testing.T) {
	now := time.Now().UTC()
	base := eventing.Envelope{
		ID: uuid.NewString(), Type: eventing.EventTypeArticleEmbeddingRequested,
		SchemaVersion: 1, AggregateType: "article", AggregateID: "42",
		OccurredAt: now, Payload: []byte("{\"article_id\":42}"),
	}
	tests := []struct {
		name string
		edit func(*eventing.Envelope)
	}{
		{name: "invalid uuid", edit: func(event *eventing.Envelope) { event.ID = "bad" }},
		{name: "wrong type", edit: func(event *eventing.Envelope) { event.Type = eventing.EventTypeArticleViewed }},
		{name: "unsupported schema", edit: func(event *eventing.Envelope) { event.SchemaVersion = 2 }},
		{name: "wrong aggregate type", edit: func(event *eventing.Envelope) { event.AggregateType = "user" }},
		{name: "missing occurred at", edit: func(event *eventing.Envelope) { event.OccurredAt = time.Time{} }},
		{name: "aggregate mismatch", edit: func(event *eventing.Envelope) { event.AggregateID = "41" }},
		{name: "zero article", edit: func(event *eventing.Envelope) {
			event.AggregateID = "0"
			event.Payload = []byte("{\"article_id\":0}")
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
			if _, err := decodeArticleEmbeddingRequest(raw); err == nil {
				t.Fatal("expected strict contract error")
			}
		})
	}
}

func consumeArticleEmbeddingTestMessage(t *testing.T, store articleEmbeddingStore, embedder embeddings.Embedder, commitErr error) (*articleEmbeddingTestReader, error) {
	t.Helper()
	stopErr := errors.New("test reader stopped")
	reader := &articleEmbeddingTestReader{
		messages:  []kafka.Message{articleEmbeddingTestMessage(t, 42)},
		commitErr: commitErr,
		stopErr:   stopErr,
	}
	return reader, consumeArticleEmbeddingMessages(context.Background(), reader, embedder, store, "v1")
}

func TestArticleEmbeddingConsumerGeneratesMissingProjection(t *testing.T) {
	store := newArticleEmbeddingTestStore()
	embedder := &articleEmbeddingTestEmbedder{}
	reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 {
		t.Fatalf("err=%v commits=%d", err, reader.commitCalls)
	}
	if embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("provider_calls=%d upserts=%d", embedder.calls, len(store.upserted))
	}
	got := store.upserted[0]
	if got.ArticleID != 42 || got.Version != "v1" || got.Model != "test-model" || got.Dimensions != 2 ||
		got.ContentHash != embeddings.ArticleEmbeddingContentHash("Title", "Body") {
		t.Fatalf("embedding=%#v", got)
	}
}

func TestArticleEmbeddingConsumerSkipsCurrentProjection(t *testing.T) {
	store := newArticleEmbeddingTestStore()
	store.embeddingErr = nil
	store.embedding = models.ArticleEmbedding{
		ArticleID: 42, Version: "v1", ContentHash: embeddings.ArticleEmbeddingContentHash("Title", "Body"),
	}
	embedder := &articleEmbeddingTestEmbedder{}
	reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
		t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
}

func TestArticleEmbeddingConsumerRegeneratesStaleVersionAndContent(t *testing.T) {
	tests := []struct {
		name    string
		version string
		hash    string
	}{
		{name: "stale version", version: "old", hash: embeddings.ArticleEmbeddingContentHash("Title", "Body")},
		{name: "stale content", version: "v1", hash: "old-hash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newArticleEmbeddingTestStore()
			store.embeddingErr = nil
			store.embedding = models.ArticleEmbedding{ArticleID: 42, Version: test.version, ContentHash: test.hash}
			embedder := &articleEmbeddingTestEmbedder{}
			reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
		})
	}
}

func TestArticleEmbeddingConsumerCommitsMissingAndUnavailableArticles(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		store := newArticleEmbeddingTestStore()
		store.articleErr = gorm.ErrRecordNotFound
		embedder := &articleEmbeddingTestEmbedder{}
		reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
			t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
		}
	})
	t.Run("unpublished", func(t *testing.T) {
		store := newArticleEmbeddingTestStore()
		store.article.PublicationState = "draft"
		embedder := &articleEmbeddingTestEmbedder{}
		reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 1 || embedder.calls != 0 || len(store.upserted) != 0 {
			t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
		}
	})
}

func TestArticleEmbeddingConsumerProviderRetryFailuresDoNotCommit(t *testing.T) {
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
			store := newArticleEmbeddingTestStore()
			embedder := &articleEmbeddingTestEmbedder{err: test.err}
			reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 0 || embedder.calls != 1 || len(store.upserted) != 0 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
		})
	}
}

func TestArticleEmbeddingConsumerTerminalProviderErrorsCommit(t *testing.T) {
	for _, status := range []int{400, 413, 422} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			store := newArticleEmbeddingTestStore()
			embedder := &articleEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: status}}
			reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
			if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 0 {
				t.Fatalf("err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
			}
		})
	}
}

func TestArticleEmbeddingConsumerDBReadAndUpsertFailuresDoNotCommit(t *testing.T) {
	t.Run("article read", func(t *testing.T) {
		store := newArticleEmbeddingTestStore()
		store.articleErr = errors.New("article read failed")
		embedder := &articleEmbeddingTestEmbedder{}
		reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 0 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
	t.Run("projection read", func(t *testing.T) {
		store := newArticleEmbeddingTestStore()
		store.embeddingErr = errors.New("projection read failed")
		embedder := &articleEmbeddingTestEmbedder{}
		reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 0 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
	t.Run("upsert", func(t *testing.T) {
		store := newArticleEmbeddingTestStore()
		store.upsertErr = errors.New("upsert failed")
		embedder := &articleEmbeddingTestEmbedder{}
		reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
		if err == nil || reader.commitCalls != 0 || embedder.calls != 1 {
			t.Fatalf("err=%v commits=%d provider=%d", err, reader.commitCalls, embedder.calls)
		}
	})
}

func TestArticleEmbeddingConsumerCommitFailureRedeliverySkipsProvider(t *testing.T) {
	store := newArticleEmbeddingTestStore()
	embedder := &articleEmbeddingTestEmbedder{}
	commitErr := errors.New("commit failed")
	reader, err := consumeArticleEmbeddingTestMessage(t, store, embedder, commitErr)
	if !errors.Is(err, commitErr) || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("first err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}

	reader, err = consumeArticleEmbeddingTestMessage(t, store, embedder, nil)
	if err == nil || reader.commitCalls != 1 || embedder.calls != 1 || len(store.upserted) != 1 {
		t.Fatalf("redelivery err=%v commits=%d provider=%d upserts=%d", err, reader.commitCalls, embedder.calls, len(store.upserted))
	}
}
