package tasks

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserBehaviorProjectionRecoverySemanticsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostBehavior{},
		&models.PostReaction{}, &models.UserRecoProfileDirty{}, &models.OutboxEvent{},
	); err != nil {
		t.Fatal(err)
	}

	consumerName := "test-user-behavior-recovery-" + uuid.NewString()
	kafkaConfig := config.KafkaConfig{UserBehaviorGroupID: consumerName, ActivityEventsTopic: "goexchange.activity.events.v1"}
	author := models.User{Username: "user-behavior-recovery-author-" + uuid.NewString(), Password: "test"}
	viewer := models.User{Username: "user-behavior-recovery-viewer-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	viewPost := models.Post{AuthorID: author.ID, Content: "recovery view", Visibility: "public"}
	reactionPost := models.Post{AuthorID: author.ID, Content: "recovery reaction", Visibility: "public"}
	stalePost := models.Post{AuthorID: author.ID, Content: "recovery stale reaction", Visibility: "public"}
	for _, post := range []*models.Post{&viewPost, &reactionPost, &stalePost} {
		if err := db.Create(post).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.Where("consumer_name = ?", consumerName).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("aggregate_type = ? AND aggregate_id IN ?", "post_reaction", []string{
			fmt.Sprintf("%d:%d", viewer.ID, reactionPost.ID),
		}).Delete(&models.OutboxEvent{})
		db.Unscoped().Where("user_id IN ?", []uint{author.ID, viewer.ID}).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ? OR post_id IN ?", viewer.ID, []uint{viewPost.ID, reactionPost.ID, stalePost.ID}).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id = ? AND post_id IN ?", viewer.ID, []uint{reactionPost.ID, stalePost.ID}).Delete(&models.PostReaction{})
		db.Unscoped().Where("id IN ?", []uint{viewPost.ID, reactionPost.ID, stalePost.ID}).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{author.ID, viewer.ID}).Delete(&models.User{})
	})

	baseAt := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	viewEvent := mustPostViewedEvent(t, uuid.NewString(), viewer.ID, viewPost.ID, baseAt)
	viewRecord, err := decodeUserBehaviorMessage(userBehaviorMessage(t, viewEvent))
	if err != nil {
		t.Fatal(err)
	}
	for delivery := 0; delivery < 2; delivery++ {
		if err := applyUserBehaviorRecords(context.Background(), db, consumerName, kafkaConfig, []userBehaviorEventRecord{viewRecord}); err != nil {
			t.Fatal(err)
		}
	}
	assertViewBehaviorCount(t, db, viewer.ID, viewPost.ID, 1, baseAt)
	assertPostViewCount(t, db, viewPost.ID, 1)
	if count := countRecoveryInboxEvent(t, db, consumerName, viewEvent.ID); count != 1 {
		t.Fatalf("view inbox rows=%d want=1", count)
	}

	failureEvent := mustLikeEvent(uuid.NewString(), eventing.EventTypePostLiked, viewer.ID, reactionPost.ID, 1, baseAt.Add(time.Second))
	failureRecord, err := decodeUserBehaviorMessage(userBehaviorMessage(t, failureEvent))
	if err != nil {
		t.Fatal(err)
	}
	dropTrigger := installKafkaRecoveryInsertFailureTrigger(t, db, "outbox_events", fmt.Sprintf("NEW.event_type = '%s' AND NEW.aggregate_id = '%d:%d'", eventing.EventTypePostReactionApplied, viewer.ID, reactionPost.ID))
	err = applyUserBehaviorRecords(context.Background(), db, consumerName, kafkaConfig, []userBehaviorEventRecord{failureRecord})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
		t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, failureEvent.ID); count != 0 {
		t.Fatalf("failed reaction left inbox rows=%d want=0", count)
	}
	var reactionCount, outboxCount int64
	if err := db.Model(&models.PostReaction{}).Where("user_id = ? AND post_id = ?", viewer.ID, reactionPost.ID).Count(&reactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).Where("event_type = ? AND aggregate_id = ?", eventing.EventTypePostReactionApplied, fmt.Sprintf("%d:%d", viewer.ID, reactionPost.ID)).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if reactionCount != 0 || outboxCount != 0 {
		t.Fatalf("rolled back reaction rows=%d outbox rows=%d", reactionCount, outboxCount)
	}
	dropTrigger()
	if err := applyUserBehaviorRecords(context.Background(), db, consumerName, kafkaConfig, []userBehaviorEventRecord{failureRecord}); err != nil {
		t.Fatalf("redelivery after trigger removal: %v", err)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, failureEvent.ID); count != 1 {
		t.Fatalf("redelivered reaction inbox rows=%d want=1", count)
	}
	if err := db.Model(&models.PostReaction{}).Where("user_id = ? AND post_id = ?", viewer.ID, reactionPost.ID).Count(&reactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).Where("event_type = ? AND aggregate_id = ?", eventing.EventTypePostReactionApplied, fmt.Sprintf("%d:%d", viewer.ID, reactionPost.ID)).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if reactionCount != 1 || outboxCount != 1 {
		t.Fatalf("recovered reaction rows=%d outbox rows=%d want 1 each", reactionCount, outboxCount)
	}

	if err := db.Create(&models.PostReaction{
		UserID: viewer.ID, PostID: stalePost.ID, Reaction: models.PostReactionLike,
		Liked: true, Version: 10, UpdatedAt: baseAt, StateChangedAt: baseAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	staleEvent := mustLikeEvent("stale-reaction-9", eventing.EventTypePostUnliked, viewer.ID, stalePost.ID, 9, baseAt.Add(time.Minute))
	staleRecord, err := decodeUserBehaviorMessage(userBehaviorMessage(t, staleEvent))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyUserBehaviorRecords(context.Background(), db, consumerName, kafkaConfig, []userBehaviorEventRecord{staleRecord}); err != nil {
		t.Fatalf("stale reaction should be terminal: %v", err)
	}
	assertReaction(t, db, viewer.ID, stalePost.ID, true, 10, baseAt)
}
