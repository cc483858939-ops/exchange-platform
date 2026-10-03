package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestReactionOutboxLaterChunkRollbackAndReplayIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	rollbackFixture := errors.New("rollback isolated reaction batch fixture")
	err = db.Transaction(func(tx *gorm.DB) error {
		// All fixture tables, trigger and data are isolated and rolled back. No
		// existing application table is migrated, truncated or cleaned up.
		schema := "test_reaction_batch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if err := tx.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SET LOCAL search_path TO "` + schema + `"`).Error; err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostReaction{}, &models.UserRecoProfileDirty{}, &models.OutboxEvent{}); err != nil {
			return err
		}
		author := models.User{Username: "batch-author", Password: "test"}
		viewer := models.User{Username: "batch-viewer", Password: "test"}
		for _, user := range []*models.User{&author, &viewer} {
			if err := tx.Create(user).Error; err != nil {
				return err
			}
		}
		posts := make([]models.Post, 501)
		for i := range posts {
			posts[i] = models.Post{AuthorID: author.ID, Content: "batch fixture", Visibility: "public"}
		}
		if err := tx.Create(&posts).Error; err != nil {
			return err
		}
		records := make([]userBehaviorEventRecord, len(posts))
		for i, post := range posts {
			eventType := eventing.EventTypePostLiked
			if i%2 == 1 {
				eventType = eventing.EventTypePostUnliked
			}
			records[i] = userBehaviorEventRecord{
				Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventType, OccurredAt: time.Date(2026, 10, 3, 0, 0, 0, i, time.UTC)},
				Payload:  eventing.UserBehaviorPayload{UserID: viewer.ID, PostID: post.ID, LikeVersion: int64(i + 1)},
			}
		}
		if err := tx.Exec(`CREATE FUNCTION fail_second_outbox_chunk() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (SELECT COUNT(*) FROM outbox_events) >= 500 THEN
    RAISE EXCEPTION 'injected second Outbox chunk failure';
  END IF;
  RETURN NEW;
END $$`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TRIGGER fail_second_outbox_chunk BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION fail_second_outbox_chunk()`).Error; err != nil {
			return err
		}
		kafkaConfig := config.KafkaConfig{ActivityEventsTopic: "activity-test"}
		applyErr := applyUserBehaviorRecords(context.Background(), tx, "batch-test", kafkaConfig, records)
		if kafkaFailureClassOf(applyErr) != kafkaFailureRetryable || kafkaFailureCode(applyErr) != kafkaFailureCodeDatabaseTransaction {
			return fmt.Errorf("second chunk failure lost retry contract: %w", applyErr)
		}
		for _, table := range []string{"consumer_inboxes", "post_reaction", "outbox_events", "user_reco_profile_dirty"} {
			var count int64
			if err := tx.Table(table).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("failed batch left %d rows in %s", count, table)
			}
		}
		if err := tx.Exec(`DROP TRIGGER fail_second_outbox_chunk ON outbox_events`).Error; err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			if err := applyUserBehaviorRecords(context.Background(), tx, "batch-test", kafkaConfig, records); err != nil {
				return err
			}
			for _, table := range []string{"consumer_inboxes", "post_reaction", "outbox_events"} {
				var count int64
				if err := tx.Table(table).Count(&count).Error; err != nil {
					return err
				}
				if count != 501 {
					return fmt.Errorf("recovery/replay left %d rows in %s", count, table)
				}
			}
		}
		stale := records[0]
		stale.Envelope.ID = uuid.NewString()
		stale.Envelope.Type = eventing.EventTypePostUnliked
		if err := applyUserBehaviorRecords(context.Background(), tx, "batch-test", kafkaConfig, []userBehaviorEventRecord{stale}); err != nil {
			return err
		}
		var outboxCount int64
		if err := tx.Model(&models.OutboxEvent{}).Count(&outboxCount).Error; err != nil {
			return err
		}
		if outboxCount != 501 {
			return fmt.Errorf("stale/equal version generated activity: %d", outboxCount)
		}
		return rollbackFixture
	})
	if !errors.Is(err, rollbackFixture) {
		t.Fatal(err)
	}
}
