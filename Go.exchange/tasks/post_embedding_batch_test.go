package tasks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/models"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

var errPostEmbeddingBatchReaderStopped = errors.New("post embedding test reader stopped")

type postEmbeddingBatchTestReader struct {
	mu             sync.Mutex
	messages       []kafka.Message
	fetchErr       error
	blockWhenEmpty bool
	fetched        chan kafka.Message
	commits        [][]kafka.Message
	commitErr      error
	commitHook     func([]kafka.Message)
}

func (r *postEmbeddingBatchTestReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if err := ctx.Err(); err != nil {
		return kafka.Message{}, err
	}
	r.mu.Lock()
	if len(r.messages) > 0 {
		message := r.messages[0]
		r.messages = r.messages[1:]
		r.mu.Unlock()
		if r.fetched != nil {
			select {
			case r.fetched <- message:
			default:
			}
		}
		return message, nil
	}
	fetchErr := r.fetchErr
	blockWhenEmpty := r.blockWhenEmpty && len(r.commits) == 0
	r.mu.Unlock()
	if fetchErr != nil {
		return kafka.Message{}, fetchErr
	}
	if blockWhenEmpty {
		<-ctx.Done()
		return kafka.Message{}, ctx.Err()
	}
	return kafka.Message{}, errPostEmbeddingBatchReaderStopped
}

func (r *postEmbeddingBatchTestReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	copyOfMessages := append([]kafka.Message(nil), messages...)
	r.mu.Lock()
	r.commits = append(r.commits, copyOfMessages)
	err := r.commitErr
	hook := r.commitHook
	r.mu.Unlock()
	if hook != nil {
		hook(copyOfMessages)
	}
	return err
}

func (*postEmbeddingBatchTestReader) Close() error { return nil }

type postEmbeddingBatchTestStore struct {
	posts          map[uint]models.Post
	embeddings     map[uint]models.PostEmbedding
	writeCalls     int
	failWriteAt    int
	failWriteErr   error
	outcomeWriteAt int
	outcome        postEmbeddingWriteOutcome
}

func (s *postEmbeddingBatchTestStore) GetPost(_ context.Context, postID uint) (models.Post, error) {
	post, ok := s.posts[postID]
	if !ok {
		return models.Post{}, gorm.ErrRecordNotFound
	}
	return post, nil
}

func (s *postEmbeddingBatchTestStore) GetEmbedding(_ context.Context, postID uint, version string) (models.PostEmbedding, error) {
	embedding, ok := s.embeddings[postID]
	if !ok || embedding.Version != version {
		return models.PostEmbedding{}, gorm.ErrRecordNotFound
	}
	return embedding, nil
}

func (s *postEmbeddingBatchTestStore) CommitEmbeddingIfCurrent(_ context.Context, embedding models.PostEmbedding, _ time.Time) (postEmbeddingWriteOutcome, error) {
	s.writeCalls++
	if s.failWriteAt == s.writeCalls {
		return postEmbeddingWriteCommitted, s.failWriteErr
	}
	if s.outcomeWriteAt == s.writeCalls {
		return s.outcome, nil
	}
	s.embeddings[embedding.PostID] = embedding
	return postEmbeddingWriteCommitted, nil
}

type postEmbeddingBatchTestEmbedder struct {
	mu                 sync.Mutex
	calls              [][]string
	tooLargeAbove      int
	alwaysTooLarge     bool
	retryableErr       error
	invalidMultiResult bool
}

func (e *postEmbeddingBatchTestEmbedder) Embed(_ context.Context, texts []string) (embeddings.EmbedResult, error) {
	textCopy := append([]string(nil), texts...)
	e.mu.Lock()
	e.calls = append(e.calls, textCopy)
	e.mu.Unlock()
	if e.alwaysTooLarge || (e.tooLargeAbove > 0 && len(texts) > e.tooLargeAbove) {
		return embeddings.EmbedResult{}, &embeddings.ProviderHTTPError{StatusCode: 413}
	}
	if e.retryableErr != nil {
		return embeddings.EmbedResult{}, e.retryableErr
	}
	if e.invalidMultiResult && len(texts) > 1 {
		return embeddings.EmbedResult{Model: "test-model", Vectors: [][]float32{{1, 2}}}, nil
	}
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		var postID int
		if _, err := fmt.Sscanf(text, "Post-%d", &postID); err != nil {
			return embeddings.EmbedResult{}, err
		}
		vectors[index] = []float32{float32(postID), 7}
	}
	return embeddings.EmbedResult{Model: "test-model", Vectors: vectors}, nil
}

type postEmbeddingBatchTestPublisher struct {
	topics   []string
	messages []kafka.Message
	err      error
}

func (p *postEmbeddingBatchTestPublisher) PublishRaw(_ context.Context, topic string, messages ...kafka.Message) error {
	p.topics = append(p.topics, topic)
	p.messages = append(p.messages, messages...)
	return p.err
}

func newPostEmbeddingBatchFixture(t *testing.T, count int) (*postEmbeddingBatchTestStore, []kafka.Message) {
	t.Helper()
	store := &postEmbeddingBatchTestStore{
		posts:      make(map[uint]models.Post, count),
		embeddings: make(map[uint]models.PostEmbedding),
	}
	messages := make([]kafka.Message, count)
	for index := range messages {
		postID := uint(100 + index)
		content := fmt.Sprintf("Post-%d", postID)
		store.posts[postID] = models.Post{Model: gorm.Model{ID: postID}, Content: content, Visibility: "public"}
		messages[index] = postEmbeddingTestMessage(t, postID)
		messages[index].Offset = int64(100 + index)
	}
	return store, messages
}

func consumePostEmbeddingBatchForTest(
	ctx context.Context,
	reader *postEmbeddingBatchTestReader,
	store postEmbeddingStore,
	embedder embeddings.Embedder,
	publisher rawKafkaMessagePublisher,
	batchSize int,
	maxWait time.Duration,
	policy kafkaRetryPolicy,
) error {
	return consumePostEmbeddingMessagesWithBatchPolicy(
		ctx, reader, publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), batchSize, maxWait, policy,
	)
}

func TestPostEmbeddingBatchFullCommitsTogetherAndMapsVectors(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 5)
	for index := range messages {
		messages[index].Partition = index % 2
	}
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 5, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
		t.Fatalf("consume error=%v", err)
	}
	if len(embedder.calls) != 1 || len(embedder.calls[0]) != 5 {
		t.Fatalf("embedding calls=%v, want one call with five texts", embedder.calls)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 5 {
		t.Fatalf("commits=%v, want one commit containing all five messages", reader.commits)
	}
	for index := range messages {
		postID := uint(100 + index)
		got, ok := store.embeddings[postID]
		if !ok || len(got.Embedding.Slice()) != 2 || got.Embedding.Slice()[0] != float32(postID) {
			t.Fatalf("post %d embedding=%+v exists=%t", postID, got, ok)
		}
	}
	if reader.commits[0][0].Partition == reader.commits[0][1].Partition {
		t.Fatal("test batch did not contain messages from multiple partitions")
	}
}

func TestPostEmbeddingBatchPartialCollectionWaitsOnlyToDeadline(t *testing.T) {
	for _, count := range []int{3, 1} {
		t.Run(fmt.Sprintf("messages_%d", count), func(t *testing.T) {
			store, messages := newPostEmbeddingBatchFixture(t, count)
			reader := &postEmbeddingBatchTestReader{messages: messages, blockWhenEmpty: true}
			embedder := &postEmbeddingBatchTestEmbedder{}
			maxWait := time.Duration(config.DefaultEmbeddingBatchMaxWaitMs) * time.Millisecond
			started := time.Now()
			err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 5, maxWait, kafkaRetryPolicy{MaxAttempts: 1})
			elapsed := time.Since(started)
			if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
				t.Fatalf("consume error=%v", err)
			}
			if elapsed < 40*time.Millisecond || elapsed > 500*time.Millisecond {
				t.Fatalf("partial collection elapsed=%s, want near %s", elapsed, maxWait)
			}
			if len(embedder.calls) != 1 || len(embedder.calls[0]) != count || len(reader.commits) != 1 || len(reader.commits[0]) != count {
				t.Fatalf("calls=%v commits=%v", embedder.calls, reader.commits)
			}
		})
	}
}

func TestPostEmbeddingBatchFiltersCurrentAndMissingPosts(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 5)
	for _, postID := range []uint{101, 104} {
		post := store.posts[postID]
		store.embeddings[postID] = models.PostEmbedding{
			PostID: postID, Version: "v1", ContentHash: embeddings.PostEmbeddingContentHash(post.Content),
		}
	}
	delete(store.posts, 102)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 5, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
		t.Fatalf("consume error=%v", err)
	}
	want := []string{"Post-100", "Post-103"}
	if len(embedder.calls) != 1 || fmt.Sprint(embedder.calls[0]) != fmt.Sprint(want) {
		t.Fatalf("embedding inputs=%v want=%v", embedder.calls, want)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 5 {
		t.Fatalf("commits=%v", reader.commits)
	}
}

func TestPostEmbeddingBatchAllMissingPostsSkipProvider(t *testing.T) {
	_, messages := newPostEmbeddingBatchFixture(t, 3)
	store := &postEmbeddingBatchTestStore{posts: map[uint]models.Post{}, embeddings: map[uint]models.PostEmbedding{}}
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) || len(embedder.calls) != 0 || len(reader.commits) != 1 || len(reader.commits[0]) != 3 {
		t.Fatalf("err=%v calls=%v commits=%v", err, embedder.calls, reader.commits)
	}
}

func TestPostEmbeddingBatch413SplitsAndCommitsWithoutDLQ(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 5)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{tooLargeAbove: 2}
	publisher := &postEmbeddingBatchTestPublisher{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, publisher, 5, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
		t.Fatalf("consume error=%v", err)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 5 || len(store.embeddings) != 5 {
		t.Fatalf("commits=%v embeddings=%d", reader.commits, len(store.embeddings))
	}
	if len(publisher.messages) != 0 || len(publisher.topics) != 0 {
		t.Fatalf("413 split must not send provider failures to DLQ: topics=%v messages=%d", publisher.topics, len(publisher.messages))
	}
	if got := len(embedder.calls); got != 5 {
		t.Fatalf("provider calls=%d, want 5 bounded parent/sub-batch requests", got)
	}
}

func TestPostEmbeddingSingle413PausesWithoutCommitOrDLQ(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 1)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{alwaysTooLarge: true}
	publisher := &postEmbeddingBatchTestPublisher{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, publisher, 1, 0, kafkaRetryPolicy{MaxAttempts: 5})
	decision, ok := providerFailureDecision(err)
	if !ok || decision.Action != ProviderActionPause || len(embedder.calls) != 1 || len(reader.commits) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("decision=%+v typed=%t calls=%v commits=%v DLQ=%d err=%v", decision, ok, embedder.calls, reader.commits, len(publisher.messages), err)
	}
}

func TestPostEmbeddingBatchContractFailureSplitsAndRecovers(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 3)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{invalidMultiResult: true}
	publisher := &postEmbeddingBatchTestPublisher{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, publisher, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
		t.Fatalf("consume error=%v", err)
	}
	if len(embedder.calls) < 3 || len(store.embeddings) != 3 || len(publisher.messages) != 0 || len(reader.commits) != 1 {
		t.Fatalf("provider calls=%d embeddings=%d DLQ=%d commits=%v", len(embedder.calls), len(store.embeddings), len(publisher.messages), reader.commits)
	}
}

func TestPostEmbeddingBatchRetryableProviderFailureDoesNotCommit(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 2)
	messages[0].Partition = 0
	messages[1].Partition = 1
	reader := &postEmbeddingBatchTestReader{messages: messages}
	embedder := &postEmbeddingBatchTestEmbedder{retryableErr: &embeddings.ProviderHTTPError{StatusCode: 503}}
	publisher := &postEmbeddingBatchTestPublisher{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, publisher, 2, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	if err == nil || len(embedder.calls) != 2 || len(reader.commits) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("err=%v provider_calls=%d commits=%v DLQ=%d", err, len(embedder.calls), reader.commits, len(publisher.messages))
	}
}

func TestPostEmbeddingBatchFetchFailureSafelyCommitsFetchedMessage(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 1)
	fetchErr := errors.New("broker read failed")
	reader := &postEmbeddingBatchTestReader{messages: messages, fetchErr: fetchErr}
	embedder := &postEmbeddingBatchTestEmbedder{}
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, fetchErr) || len(reader.commits) != 1 || len(reader.commits[0]) != 1 || reader.commits[0][0].Offset != messages[0].Offset || len(embedder.calls) != 1 {
		t.Fatalf("err=%v commits=%v provider_calls=%d", err, reader.commits, len(embedder.calls))
	}
}

func TestPostEmbeddingBatchCancellationDuringCollectionDoesNotCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, messages := newPostEmbeddingBatchFixture(t, 1)
	reader := &postEmbeddingBatchTestReader{messages: messages, blockWhenEmpty: true, fetched: make(chan kafka.Message, 1)}
	go func() {
		<-reader.fetched
		cancel()
	}()
	embedder := &postEmbeddingBatchTestEmbedder{}
	err := consumePostEmbeddingBatchForTest(ctx, reader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, time.Second, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, context.Canceled) || len(reader.commits) != 0 || len(embedder.calls) != 0 {
		t.Fatalf("err=%v commits=%v provider_calls=%d", err, reader.commits, len(embedder.calls))
	}
}

func TestPostEmbeddingBatchPartialWriteReplaysIdempotently(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 3)
	store.failWriteAt = 2
	store.failWriteErr = errors.New("database write failed")
	firstReader := &postEmbeddingBatchTestReader{messages: append([]kafka.Message(nil), messages...)}
	embedder := &postEmbeddingBatchTestEmbedder{}
	firstErr := consumePostEmbeddingBatchForTest(context.Background(), firstReader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if firstErr == nil || len(firstReader.commits) != 0 || len(store.embeddings) != 1 {
		t.Fatalf("first err=%v commits=%v embeddings=%d", firstErr, firstReader.commits, len(store.embeddings))
	}
	store.failWriteAt = 0
	secondReader := &postEmbeddingBatchTestReader{messages: append([]kafka.Message(nil), messages...)}
	secondErr := consumePostEmbeddingBatchForTest(context.Background(), secondReader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(secondErr, errPostEmbeddingBatchReaderStopped) || len(secondReader.commits) != 1 || len(secondReader.commits[0]) != 3 {
		t.Fatalf("replay err=%v commits=%v", secondErr, secondReader.commits)
	}
	if len(embedder.calls) != 2 || len(embedder.calls[0]) != 3 || len(embedder.calls[1]) != 2 || len(store.embeddings) != 3 {
		t.Fatalf("provider calls=%v embeddings=%d", embedder.calls, len(store.embeddings))
	}
}

func TestPostEmbeddingBatchCommitFailureReplaySkipsProvider(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 3)
	embedder := &postEmbeddingBatchTestEmbedder{}
	commitErr := errors.New("Kafka commit failed")
	firstReader := &postEmbeddingBatchTestReader{messages: append([]kafka.Message(nil), messages...), commitErr: commitErr}
	firstErr := consumePostEmbeddingBatchForTest(context.Background(), firstReader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(firstErr, commitErr) || len(firstReader.commits) != 1 || len(store.embeddings) != 3 {
		t.Fatalf("first err=%v commits=%v embeddings=%d", firstErr, firstReader.commits, len(store.embeddings))
	}
	secondReader := &postEmbeddingBatchTestReader{messages: append([]kafka.Message(nil), messages...)}
	secondErr := consumePostEmbeddingBatchForTest(context.Background(), secondReader, store, embedder, &postEmbeddingBatchTestPublisher{}, 3, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(secondErr, errPostEmbeddingBatchReaderStopped) || len(secondReader.commits) != 1 || len(secondReader.commits[0]) != 3 {
		t.Fatalf("replay err=%v commits=%v", secondErr, secondReader.commits)
	}
	if len(embedder.calls) != 1 || len(embedder.calls[0]) != 3 {
		t.Fatalf("provider was reinvoked after commit failure: calls=%v", embedder.calls)
	}
}

func TestPostEmbeddingBatchStaleContentAndDLQFailureDoNotCommit(t *testing.T) {
	t.Run("stale content", func(t *testing.T) {
		store, messages := newPostEmbeddingBatchFixture(t, 1)
		store.outcomeWriteAt = 1
		store.outcome = postEmbeddingWriteStaleContent
		reader := &postEmbeddingBatchTestReader{messages: messages}
		err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, &postEmbeddingBatchTestEmbedder{}, &postEmbeddingBatchTestPublisher{}, 1, 0, kafkaRetryPolicy{MaxAttempts: 1})
		if !errors.Is(err, errPostEmbeddingSourceChanged) || len(reader.commits) != 0 {
			t.Fatalf("err=%v commits=%v", err, reader.commits)
		}
	})

	t.Run("DLQ publish failure", func(t *testing.T) {
		store, messages := newPostEmbeddingBatchFixture(t, 2)
		messages[0].Value = []byte("{")
		reader := &postEmbeddingBatchTestReader{messages: messages}
		publisher := &postEmbeddingBatchTestPublisher{err: errors.New("DLQ unavailable")}
		embedder := &postEmbeddingBatchTestEmbedder{}
		err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, embedder, publisher, 2, 50*time.Millisecond, kafkaRetryPolicy{MaxAttempts: 1})
		if err == nil || len(reader.commits) != 0 {
			t.Fatalf("err=%v commits=%v", err, reader.commits)
		}
	})
}

func TestPostEmbeddingBatchOneSkipsCollectionWait(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 1)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	started := time.Now()
	err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, &postEmbeddingBatchTestEmbedder{}, &postEmbeddingBatchTestPublisher{}, 1, 0, kafkaRetryPolicy{MaxAttempts: 1})
	if !errors.Is(err, errPostEmbeddingBatchReaderStopped) {
		t.Fatalf("consume error=%v", err)
	}
	if elapsed := time.Since(started); elapsed >= 20*time.Millisecond {
		t.Fatalf("single-message mode waited unexpectedly: %s", elapsed)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 1 {
		t.Fatalf("commits=%v", reader.commits)
	}
}

func TestPostEmbeddingBatchConfigRejectsInvalidPolicy(t *testing.T) {
	store, messages := newPostEmbeddingBatchFixture(t, 1)
	reader := &postEmbeddingBatchTestReader{messages: messages}
	for _, test := range []struct {
		batchSize int
		maxWait   time.Duration
	}{
		{batchSize: 0, maxWait: time.Millisecond},
		{batchSize: config.MaxEmbeddingBatchSize + 1, maxWait: time.Millisecond},
		{batchSize: 2, maxWait: 0},
		{batchSize: 2, maxWait: time.Duration(config.MaxEmbeddingBatchMaxWaitMs+1) * time.Millisecond},
	} {
		err := consumePostEmbeddingBatchForTest(context.Background(), reader, store, &postEmbeddingBatchTestEmbedder{}, &postEmbeddingBatchTestPublisher{}, test.batchSize, test.maxWait, kafkaRetryPolicy{MaxAttempts: 1})
		if err == nil {
			t.Errorf("invalid batch policy accepted: size=%d wait=%s", test.batchSize, test.maxWait)
		}
	}
}
