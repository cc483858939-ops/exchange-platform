package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/global"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type articleEmbeddingTestReader struct {
	messages  []kafka.Message
	index     int
	commits   int
	commitErr error
	stopErr   error
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

func (r *articleEmbeddingTestReader) CommitMessages(context.Context, ...kafka.Message) error {
	r.commits++
	return r.commitErr
}

func (*articleEmbeddingTestReader) Close() error { return nil }

type articleEmbeddingTestEmbedder struct {
	calls int
	err   error
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
	if err := consumeArticleEmbeddingMessages(context.Background(), reader, &articleEmbeddingTestEmbedder{}); !errors.Is(err, reader.stopErr) {
		t.Fatalf("err=%v", err)
	}
	if reader.commits != len(reader.messages) {
		t.Fatalf("commits=%d want=%d", reader.commits, len(reader.messages))
	}
}

func TestConsumeArticleEmbeddingMessagesDoesNotCommitWhenProcessingFails(t *testing.T) {
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = nil
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "test-version"}}
	t.Cleanup(func() { global.Db, config.AppConfig = originalDB, originalConfig })

	reader := &articleEmbeddingTestReader{messages: []kafka.Message{articleEmbeddingTestMessage(t, 42)}, stopErr: errors.New("should not fetch again")}
	err := consumeArticleEmbeddingMessages(context.Background(), reader, &articleEmbeddingTestEmbedder{})
	if err == nil || reader.commits != 0 {
		t.Fatalf("err=%v commits=%d", err, reader.commits)
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
