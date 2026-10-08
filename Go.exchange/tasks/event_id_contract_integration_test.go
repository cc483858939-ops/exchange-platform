package tasks

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

func TestCanonicalEventIDProjectionAndReplayIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("event_id_projection_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema + ", public").Error; err != nil {
		t.Fatal(err)
	}
	if err := initialize.RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	cfg := replayIntegrationKafkaConfig()
	cfg.UserBehaviorGroupID = "event-id-behavior"
	cfg.NotificationGroupID = "event-id-notification"
	cfg.LikeSnapshotGroupID = "event-id-snapshot"
	oldConfig, oldWorkerDB := config.AppConfig, global.WorkerDb
	config.AppConfig, global.WorkerDb = &config.Config{Kafka: cfg}, tx
	t.Cleanup(func() { config.AppConfig, global.WorkerDb = oldConfig, oldWorkerDB })
	author := models.User{Model: gorm.Model{ID: 100000001}, Username: "event-id-author", Password: "test"}
	actor := models.User{Model: gorm.Model{ID: 100000000}, Username: "event-id-actor", Password: "test"}
	for _, user := range []*models.User{&author, &actor} {
		if err := tx.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	post := models.Post{Model: gorm.Model{ID: 100000000}, AuthorID: author.ID, Content: "event ID projection", Visibility: "public"}
	if err := tx.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	view, err := eventing.NewPostViewedEnvelope(uuid.NewString(), actor.ID, post.ID, at, "feed")
	if err != nil {
		t.Fatal(err)
	}
	paddedView := view
	paddedView.ID = " \t" + view.ID + "\n"
	paddedMessage := envelopeKafkaMessage(t, cfg.UserBehaviorTopic, paddedView, nil, at)
	canonicalMessage := envelopeKafkaMessage(t, cfg.UserBehaviorTopic, view, nil, at)
	records, err := classifyUserBehaviorBatch(t.Context(), nil, cfg, []kafka.Message{paddedMessage, canonicalMessage})
	if err != nil || len(records) != 1 || records[0].Envelope.ID != view.ID {
		t.Fatalf("same-batch canonical view records=%+v err=%v", records, err)
	}
	if err := applyUserBehaviorRecords(t.Context(), tx, cfg.UserBehaviorGroupID, cfg, records); err != nil {
		t.Fatal(err)
	}
	// Replay keeps the original padded bytes; decoding must restore the same key.
	for _, message := range []kafka.Message{canonicalMessage, replayIntegrationSource(t, cfg, kafkaConsumerUserBehaviorProjection, paddedMessage)} {
		record, err := decodeUserBehaviorMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyUserBehaviorRecords(t.Context(), tx, cfg.UserBehaviorGroupID, cfg, []userBehaviorEventRecord{record}); err != nil {
			t.Fatal(err)
		}
	}
	assertPostViewCount(t, tx, post.ID, 1)
	assertViewBehaviorCount(t, tx, actor.ID, post.ID, 1, at)
	if n := countRecoveryInboxEvent(t, tx, cfg.UserBehaviorGroupID, view.ID); n != 1 {
		t.Fatalf("canonical view Inbox rows=%d", n)
	}

	likeID := fmt.Sprintf("like-state:%d:%d:%d", actor.ID, post.ID, 100000000)
	like, err := eventing.NewLikeBehaviorEnvelope(likeID, actor.ID, post.ID, "like", 100000000, at)
	if err != nil {
		t.Fatal(err)
	}
	like.ID = " " + like.ID + "\t"
	likeMessage := envelopeKafkaMessage(t, cfg.UserBehaviorTopic, like, nil, at)
	for _, message := range []kafka.Message{likeMessage, replayIntegrationSource(t, cfg, kafkaConsumerUserBehaviorProjection, likeMessage)} {
		record, err := decodeUserBehaviorMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyUserBehaviorRecords(t.Context(), tx, cfg.UserBehaviorGroupID, cfg, []userBehaviorEventRecord{record}); err != nil {
			t.Fatal(err)
		}
	}
	assertReaction(t, tx, actor.ID, post.ID, true, 100000000, at)
	if n := countRecoveryInboxEvent(t, tx, cfg.UserBehaviorGroupID, likeID); n != 1 {
		t.Fatalf("long Like Inbox rows=%d", n)
	}
	var outboxCount int64
	if err := tx.Model(&models.OutboxEvent{}).Where("event_type = ?", eventing.EventTypePostReactionApplied).Count(&outboxCount).Error; err != nil || outboxCount != 1 {
		t.Fatalf("reaction Outbox rows=%d err=%v", outboxCount, err)
	}

	snapshot, err := eventing.NewLikeSnapshotEnvelope(post.ID, 23, 100000000)
	if err != nil {
		t.Fatal(err)
	}
	snapshotID := snapshot.ID
	snapshot.ID = "\t" + snapshot.ID + " "
	snapshotMessage := envelopeKafkaMessage(t, cfg.LikeSnapshotTopic, snapshot, nil, at)
	for _, message := range []kafka.Message{snapshotMessage, replayIntegrationSource(t, cfg, kafkaConsumerLikeSnapshotProjection, snapshotMessage)} {
		if err := processLikeSnapshotMessage(t.Context(), tx, nil, cfg, message); err != nil {
			t.Fatal(err)
		}
	}
	assertLikeSnapshotPostState(t, tx, post.ID, 23, 100000000)
	if n := countRecoveryInboxEvent(t, tx, cfg.LikeSnapshotGroupID, snapshotID); n != 1 {
		t.Fatalf("long snapshot Inbox rows=%d", n)
	}

	follow := notificationFollowEnvelope(t, uuid.NewString(), 99, actor.ID, author.ID, at)
	paddedFollow := follow
	paddedFollow.ID = " \t" + follow.ID + "\n"
	paddedFollowMessage := envelopeKafkaMessage(t, cfg.ActivityEventsTopic, paddedFollow, nil, at)
	canonicalFollowMessage := envelopeKafkaMessage(t, cfg.ActivityEventsTopic, follow, nil, at)
	if err := processNotificationBatch(context.Background(), []kafka.Message{paddedFollowMessage, canonicalFollowMessage}, nil); err != nil {
		t.Fatal(err)
	}
	var notification models.Notification
	if err := tx.Where("recipient_id = ?", author.ID).First(&notification).Error; err != nil || notification.SourceVersion != 99 {
		t.Fatalf("padded follow did not project: %+v err=%v", notification, err)
	}
	// An acknowledged notification must stay read after canonical/padded replay.
	if err := tx.Model(&notification).Update("read_at", at.Add(time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	for _, message := range []kafka.Message{canonicalFollowMessage, replayIntegrationSource(t, cfg, kafkaConsumerNotificationProjection, paddedFollowMessage)} {
		if err := processNotificationBatch(t.Context(), []kafka.Message{message}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var notifications []models.Notification
	if err := tx.Where("recipient_id = ?", author.ID).Find(&notifications).Error; err != nil || len(notifications) != 1 || notifications[0].ReadAt == nil {
		t.Fatalf("notification replay state=%+v err=%v", notifications, err)
	}
	if n := countRecoveryInboxEvent(t, tx, cfg.NotificationGroupID, follow.ID); n != 1 {
		t.Fatalf("canonical notification Inbox rows=%d", n)
	}
}
