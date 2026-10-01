package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func createQuoteNotificationTarget(t *testing.T, db *gorm.DB, authorID uint) models.Post {
	t.Helper()
	post := models.Post{AuthorID: authorID, Content: "quote target", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	return post
}

func TestQuoteCreatePersistsCanonicalActivityOutboxIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var quote models.Post
	if err := persistPostGraph(context.Background(), &quote, fixture.users[1].ID, "quote content", createPostRequest{
		Content: "quote content", QuotePostID: &target.ID,
	}, nil, now); err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, quote)

	var rows []models.OutboxEvent
	if err := db.Where("aggregate_id = ? AND event_type = ?", strconv.FormatUint(uint64(quote.ID), 10), eventing.EventTypeQuoteCreated).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("quote activity rows=%d want=1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Topic != testActivityTopic || row.PartitionKey != strconv.FormatUint(uint64(target.ID), 10) || row.SchemaVersion != 1 || row.AggregateType != "post" || row.AggregateID != strconv.FormatUint(uint64(quote.ID), 10) {
		t.Fatalf("outbox row=%+v", row)
	}
	envelope, err := eventing.DecodeEnvelope([]byte(row.Message))
	if err != nil {
		t.Fatal(err)
	}
	var payload eventing.QuoteCreatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != eventing.EventTypeQuoteCreated || envelope.AggregateID != strconv.FormatUint(uint64(quote.ID), 10) || !envelope.OccurredAt.Equal(now) ||
		payload.QuotePostID != quote.ID || payload.TargetPostID != target.ID || payload.ActorID != fixture.users[1].ID || payload.TargetAuthorID != fixture.users[0].ID || !payload.CreatedAt.Equal(now) {
		t.Fatalf("envelope=%+v payload=%+v", envelope, payload)
	}
}

func TestQuoteOutboxInsertFailureRollsBackQuoteIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)
	installRejectingOutboxInsertTrigger(t, db)

	var quote models.Post
	err := persistPostGraph(context.Background(), &quote, fixture.users[1].ID, "quote rollback", createPostRequest{
		Content: "quote rollback", QuotePostID: &target.ID,
	}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("quote persist unexpectedly succeeded with a failing activity outbox")
	}
	if quote.ID == 0 {
		t.Fatal("quote ID was not assigned before outbox failure")
	}
	fixture.posts = append(fixture.posts, quote)
	var quoteCount, outboxCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("id = ?", quote.ID).Count(&quoteCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).Where("aggregate_id = ? AND event_type = ?", strconv.FormatUint(uint64(quote.ID), 10), eventing.EventTypeQuoteCreated).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if quoteCount != 0 || outboxCount != 0 {
		t.Fatalf("rolled-back quote rows=%d activity outbox rows=%d, want 0/0", quoteCount, outboxCount)
	}
}

func TestQuoteClientPublishReplayDoesNotDuplicateActivityIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false
	target := createQuoteNotificationTarget(t, db, fixture.users[0].ID)
	fixture.posts = append(fixture.posts, target)
	key := uuid.New()
	body := `{"content":"idempotent quote","quote_post_id":` + strconv.FormatUint(uint64(target.ID), 10) + `}`
	ctx, recorder := newClientPublishIntegrationContext(body, fixture.users[1].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("first quote status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, first.ID)
	ctx, recorder = newClientPublishIntegrationContext(body, fixture.users[1].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	replay := decodeCreatedPost(t, recorder)
	if replay.ID != first.ID {
		t.Fatalf("replay post id=%d want=%d", replay.ID, first.ID)
	}
	var postCount, eventCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("author_id = ? AND client_publish_id = ?", fixture.users[1].ID, key).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).Where("aggregate_id = ? AND event_type = ?", strconv.FormatUint(uint64(first.ID), 10), eventing.EventTypeQuoteCreated).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 1 || eventCount != 1 {
		t.Fatalf("idempotent quote rows=%d activity facts=%d, want 1/1", postCount, eventCount)
	}
}
