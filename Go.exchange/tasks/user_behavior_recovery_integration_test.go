package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func TestUserBehaviorProjectionRecoverySemanticsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
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

func TestUserBehaviorViewedMissingPostRecoveryIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostBehavior{},
		&models.PostReaction{}, &models.UserRecoProfileDirty{}, &models.OutboxEvent{},
	); err != nil {
		t.Fatal(err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback User Behavior recovery integration transaction: %v", err)
		}
	})

	consumerName := "test-user-behavior-view-recovery-" + uuid.NewString()
	kafkaConfig := config.KafkaConfig{
		UserBehaviorGroupID: consumerName,
		ActivityEventsTopic: "goexchange.user.behavior.v1",
		ConsumerDLQTopic:    "goexchange.consumer.dlq.v1",
	}
	author := models.User{Username: "user-behavior-view-author-" + uuid.NewString(), Password: "test"}
	viewer := models.User{Username: "user-behavior-view-viewer-" + uuid.NewString(), Password: "test"}
	if err := tx.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: author.ID, Content: "view recovery target", Visibility: "public"}
	missingPost := models.Post{AuthorID: author.ID, Content: "post removed before view", Visibility: "public"}
	if err := tx.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(&missingPost).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Unscoped().Delete(&models.Post{}, missingPost.ID).Error; err != nil {
		t.Fatal(err)
	}

	baseAt := time.Now().UTC().Truncate(time.Microsecond)
	messageForView := func(offset int64, eventID string, postID uint, occurredAt time.Time) kafka.Message {
		message := userBehaviorMessage(t, mustPostViewedEvent(t, eventID, viewer.ID, postID, occurredAt))
		message.Topic = kafkaConfig.ActivityEventsTopic
		message.Partition = 4
		message.Offset = offset
		message.Key = []byte(eventID)
		message.Time = occurredAt
		return message
	}
	first := messageForView(90, "view-recovery-before", post.ID, baseAt)
	bad := messageForView(91, "view-recovery-missing-post", missingPost.ID, baseAt.Add(time.Second))
	last := messageForView(92, "view-recovery-after", post.ID, baseAt.Add(2*time.Second))
	publisher := &fakeRawKafkaMessagePublisher{}
	consumeBatch := func() (*fakeUserBehaviorReader, error) {
		reader := &fakeUserBehaviorReader{messages: []kafka.Message{first, bad, last}}
		err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, tx, kafkaConfig, kafkaRetryPolicy{MaxAttempts: 1}, applyUserBehaviorRecords, lookupKafkaForeignKeyParent)
		return reader, err
	}

	reader, err := consumeBatch()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("first delivery consume error=%v", err)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 3 || len(publisher.messages) != 1 {
		t.Fatalf("first delivery commits=%#v DLQ=%d", reader.commits, len(publisher.messages))
	}
	deadLetter, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.topics[0] != kafkaConfig.ConsumerDLQTopic || deadLetter.Failure.Class != string(kafkaFailurePermanent) || deadLetter.Failure.Code != kafkaFailureCodeDatabaseForeignKey {
		t.Fatalf("DLQ topic=%q failure=%+v", publisher.topics[0], deadLetter.Failure)
	}
	if deadLetter.Source.Topic != bad.Topic || deadLetter.Source.Partition != bad.Partition || deadLetter.Source.Offset != bad.Offset || string(deadLetter.Source.Value) != string(bad.Value) {
		t.Fatalf("DLQ source does not match bad view message: %+v", deadLetter.Source)
	}
	assertViewBehaviorCount(t, tx, viewer.ID, post.ID, 2, baseAt.Add(2*time.Second))
	assertPostViewCount(t, tx, post.ID, 2)
	for _, eventID := range []string{"view-recovery-before", "view-recovery-after"} {
		if count := countRecoveryInboxEvent(t, tx, consumerName, eventID); count != 1 {
			t.Fatalf("view inbox rows for %s=%d want=1", eventID, count)
		}
	}
	if count := countRecoveryInboxEvent(t, tx, consumerName, "view-recovery-missing-post"); count != 0 {
		t.Fatalf("missing Post view inbox rows=%d want=0", count)
	}

	reader, err = consumeBatch()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("redelivery consume error=%v", err)
	}
	if len(reader.commits) != 1 || len(reader.commits[0]) != 3 || len(publisher.messages) != 2 {
		t.Fatalf("redelivery commits=%#v DLQ=%d", reader.commits, len(publisher.messages))
	}
	assertViewBehaviorCount(t, tx, viewer.ID, post.ID, 2, baseAt.Add(2*time.Second))
	assertPostViewCount(t, tx, post.ID, 2)
	for _, eventID := range []string{"view-recovery-before", "view-recovery-after"} {
		if count := countRecoveryInboxEvent(t, tx, consumerName, eventID); count != 1 {
			t.Fatalf("redelivered view inbox rows for %s=%d want=1", eventID, count)
		}
	}
}
