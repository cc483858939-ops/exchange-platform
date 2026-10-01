package controllers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func readPostQuoteCount(t *testing.T, db *gorm.DB, postID uint) int64 {
	t.Helper()
	var post models.Post
	if err := db.Unscoped().Select("id", "quote_count").First(&post, postID).Error; err != nil {
		t.Fatalf("read Post %d quote_count: %v", postID, err)
	}
	return post.QuoteCount
}

func persistQuoteForCountTest(t *testing.T, targetID, authorID uint, content string) models.Post {
	t.Helper()
	var quote models.Post
	if err := persistPostGraph(context.Background(), &quote, authorID, content, createPostRequest{
		Content: content, QuotePostID: &targetID,
	}, nil, time.Now().UTC()); err != nil {
		t.Fatalf("persist Quote %q: %v", content, err)
	}
	return quote
}

func TestQuoteCountTracksMultipleAndSelfQuotesIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)
	if got := readPostQuoteCount(t, db, target.ID); got != 0 {
		t.Fatalf("initial quote_count=%d want=0", got)
	}

	first := persistQuoteForCountTest(t, target.ID, fixture.users[1].ID, "first quote")
	second := persistQuoteForCountTest(t, target.ID, fixture.users[1].ID, "second quote")
	fixture.posts = append(fixture.posts, first, second)
	if got := readPostQuoteCount(t, db, target.ID); got != 2 {
		t.Fatalf("quote_count after two Quotes=%d want=2", got)
	}
	selfQuote := persistQuoteForCountTest(t, target.ID, fixture.users[0].ID, "self quote")
	fixture.posts = append(fixture.posts, selfQuote)
	if got := readPostQuoteCount(t, db, target.ID); got != 3 {
		t.Fatalf("quote_count after self Quote=%d want=3", got)
	}
	var activeQuotes int64
	if err := db.Model(&models.Post{}).Where("quote_post_id = ? AND deleted_at IS NULL", target.ID).Count(&activeQuotes).Error; err != nil {
		t.Fatal(err)
	}
	if activeQuotes != 3 {
		t.Fatalf("active canonical Quotes=%d quote_count=%d", activeQuotes, readPostQuoteCount(t, db, target.ID))
	}
}

func TestQuoteCountIncrementFailureRollsBackQuoteAndOutboxIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)

	originalIncrement := incrementPostQuoteCount
	incrementPostQuoteCount = func(tx *gorm.DB, postID uint) (int64, error) {
		rows, err := originalIncrement(tx, postID)
		if err != nil {
			return rows, err
		}
		return rows, errors.New("injected quote counter failure after update")
	}
	t.Cleanup(func() { incrementPostQuoteCount = originalIncrement })

	var quote models.Post
	err := persistPostGraph(context.Background(), &quote, fixture.users[1].ID, "counter rollback", createPostRequest{
		Content: "counter rollback", QuotePostID: &target.ID,
	}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("Quote creation succeeded after injected counter failure")
	}
	var quoteRows, outboxRows int64
	if err := db.Unscoped().Model(&models.Post{}).Where("id = ?", quote.ID).Count(&quoteRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).Where("aggregate_id = ? AND event_type = ?", fmt.Sprint(quote.ID), eventing.EventTypeQuoteCreated).Count(&outboxRows).Error; err != nil {
		t.Fatal(err)
	}
	if quoteRows != 0 || outboxRows != 0 || readPostQuoteCount(t, db, target.ID) != 0 {
		t.Fatalf("rollback left quote rows=%d outbox rows=%d target quote_count=%d", quoteRows, outboxRows, readPostQuoteCount(t, db, target.ID))
	}
}

func TestConcurrentQuoteCreationPreservesCanonicalCountIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)

	const concurrentQuotes = 5
	start := make(chan struct{})
	var ready sync.WaitGroup
	var workers sync.WaitGroup
	results := make(chan struct {
		post models.Post
		err  error
	}, concurrentQuotes)
	for index := range concurrentQuotes {
		ready.Add(1)
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			ready.Done()
			<-start
			var quote models.Post
			content := fmt.Sprintf("concurrent quote %d", index)
			err := persistPostGraph(context.Background(), &quote, fixture.users[1].ID, content, createPostRequest{
				Content: content, QuotePostID: &target.ID,
			}, nil, time.Now().UTC())
			results <- struct {
				post models.Post
				err  error
			}{post: quote, err: err}
		}(index)
	}
	ready.Wait()
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Quote creation failed: %v", result.err)
		}
		fixture.posts = append(fixture.posts, result.post)
	}

	var activeQuotes int64
	if err := db.Model(&models.Post{}).Where("quote_post_id = ? AND deleted_at IS NULL", target.ID).Count(&activeQuotes).Error; err != nil {
		t.Fatal(err)
	}
	got := readPostQuoteCount(t, db, target.ID)
	if got != concurrentQuotes || got != activeQuotes {
		t.Fatalf("concurrent active Quotes=%d quote_count=%d want=%d", activeQuotes, got, concurrentQuotes)
	}
}

func TestDeletingQuoteUpdatesCountIncludingSoftDeletedTargetIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)
	first := persistQuoteForCountTest(t, target.ID, fixture.users[1].ID, "delete quote first")
	second := persistQuoteForCountTest(t, target.ID, fixture.users[1].ID, "delete quote second")
	third := persistQuoteForCountTest(t, target.ID, fixture.users[1].ID, "delete quote third")
	fixture.posts = append(fixture.posts, first, second, third)
	if got := readPostQuoteCount(t, db, target.ID); got != 3 {
		t.Fatalf("initial quote_count=%d want=3", got)
	}

	result, err := deletePostInTransactionFromDB(context.Background(), first.ID, fixture.users[1].ID)
	if err != nil {
		t.Fatalf("delete first Quote: %v", err)
	}
	if result.QuoteTargetPostID == nil || *result.QuoteTargetPostID != target.ID {
		t.Fatalf("delete result quote target=%v want=%d", result.QuoteTargetPostID, target.ID)
	}
	if got := readPostQuoteCount(t, db, target.ID); got != 2 {
		t.Fatalf("quote_count after deleting one of three=%d want=2", got)
	}

	if err := db.Delete(&target).Error; err != nil {
		t.Fatalf("soft-delete Quote target: %v", err)
	}
	if got := readPostQuoteCount(t, db, target.ID); got != 2 {
		t.Fatalf("deleting target changed incoming quote_count to %d want=2", got)
	}
	if _, err := deletePostInTransactionFromDB(context.Background(), second.ID, fixture.users[1].ID); err != nil {
		t.Fatalf("delete Quote for soft-deleted target: %v", err)
	}
	if got := readPostQuoteCount(t, db, target.ID); got != 1 {
		t.Fatalf("quote_count after deleting Quote for soft-deleted target=%d want=1", got)
	}
}

func TestDeletingQuoteUnderflowRollsBackIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	quote := models.Post{AuthorID: fixture.users[1].ID, Content: "inconsistent quote", Visibility: "public", QuotePostID: &target.ID}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, target, quote)

	_, err := deletePostInTransactionFromDB(context.Background(), quote.ID, fixture.users[1].ID)
	if !errors.Is(err, errPostQuoteCountConsistency) {
		t.Fatalf("delete error=%v want quote count consistency error", err)
	}
	var storedQuote models.Post
	if err := db.First(&storedQuote, quote.ID).Error; err != nil {
		t.Fatalf("inconsistent Quote was not rolled back: %v", err)
	}
	if storedQuote.DeletedAt.Valid || readPostQuoteCount(t, db, target.ID) != 0 {
		t.Fatalf("underflow delete changed state: deleted_at=%v quote_count=%d", storedQuote.DeletedAt, readPostQuoteCount(t, db, target.ID))
	}
}
