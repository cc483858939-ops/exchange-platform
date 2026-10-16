package tasks

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
)

func TestPostMentionNotificationProjectionIsValidatedAndReadIdempotentIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.Notification{}, &models.ConsumerInbox{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	oldDB, oldConfig := global.WorkerDb, config.AppConfig
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback mention notification projection transaction: %v", err)
		}
		global.WorkerDb, config.AppConfig = oldDB, oldConfig
	})
	for _, statement := range []string{
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_type",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (notification_type IN ('post_liked', 'post_replied', 'post_quoted', 'user_followed', 'post_mentioned'))",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_shape",
		`ALTER TABLE notifications ADD CONSTRAINT chk_notifications_shape CHECK (
  (notification_type = 'post_liked' AND post_id IS NOT NULL AND source_version > 0) OR
  (notification_type = 'post_replied' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_quoted' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_mentioned' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'user_followed' AND post_id IS NULL AND source_version > 0)
)`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatalf("prepare mention projection schema: %v", err)
		}
	}

	global.WorkerDb = tx
	groupID := "mention-projection-" + uuid.NewString()
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{NotificationGroupID: groupID}}
	actor := models.User{Username: "mp-actor-" + uuid.NewString(), Password: "test"}
	recipient := models.User{Username: "mp-recipient-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "mp-other-" + uuid.NewString(), Password: "test"}
	if err := tx.Create(&[]*models.User{&actor, &recipient, &other}).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: actor.ID, Content: "hello @" + recipient.Username, Visibility: "public"}
	if err := tx.Create(&post).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	makeRecord := func(eventID string, actorID, recipientID uint) notificationActivityRecord {
		t.Helper()
		envelope, err := eventing.NewPostMentionedEnvelope(eventID, eventing.PostMentionedPayload{
			PostID: post.ID, ActorID: actorID, RecipientID: recipientID, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		message := notificationMessage(t, envelope)
		record, err := decodeNotificationActivity(message)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	record := makeRecord(uuid.NewString(), actor.ID, recipient.ID)
	if err := applyNotificationRecords(context.Background(), []notificationActivityRecord{record}); err != nil {
		t.Fatal(err)
	}
	var projected models.Notification
	if err := tx.Where("dedupe_key = ?", "post_mentioned:"+uintString(post.ID)+":"+uintString(recipient.ID)).First(&projected).Error; err != nil {
		t.Fatal(err)
	}
	readAt := now.Add(time.Minute)
	if err := tx.Model(&projected).Update("read_at", readAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyNotificationRecords(context.Background(), []notificationActivityRecord{record}); err != nil {
		t.Fatal(err)
	}
	replay := makeRecord(uuid.NewString(), actor.ID, recipient.ID)
	if err := applyNotificationRecords(context.Background(), []notificationActivityRecord{replay}); err != nil {
		t.Fatal(err)
	}
	var rows []models.Notification
	if err := tx.Where("dedupe_key = ?", projected.DedupeKey).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ReadAt == nil || !rows[0].ReadAt.Equal(readAt) {
		t.Fatalf("duplicate mention projection changed read state: %+v", rows)
	}

	wrongRecipient := makeRecord(uuid.NewString(), actor.ID, other.ID)
	if err := applyNotificationRecords(context.Background(), []notificationActivityRecord{wrongRecipient}); err != nil {
		t.Fatal(err)
	}
	var falseMentionCount int64
	if err := tx.Model(&models.Notification{}).Where("recipient_id = ? AND notification_type = ?", other.ID, models.NotificationTypePostMentioned).Count(&falseMentionCount).Error; err != nil || falseMentionCount != 0 {
		t.Fatalf("body-mismatched mention notifications=%d err=%v", falseMentionCount, err)
	}

	badActor := makeRecord(uuid.NewString(), recipient.ID, other.ID)
	if err := applyNotificationRecords(context.Background(), []notificationActivityRecord{badActor}); err != nil {
		t.Fatal(err)
	}
}
