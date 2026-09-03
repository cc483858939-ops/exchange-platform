package controllers

import (
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestActivityOutboxFailureRollsBackCanonicalReplyAndFollowIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Post{}, &models.Post{}, &models.PostBehavior{}, &models.UserRecoProfileDirty{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ActivityEventsTopic: "goexchange.activity.events.v1"}}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	author := models.User{Username: "activity-author-" + uuid.NewString(), Password: "test"}
	commenter := models.User{Username: "activity-commenter-" + uuid.NewString(), Password: "test"}
	follower := models.User{Username: "activity-follower-" + uuid.NewString(), Password: "test"}
	followed := models.User{Username: "activity-followed-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&author, &commenter, &follower, &followed}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	article := models.Post{Model: gorm.Model{CreatedAt: now, UpdatedAt: now}, AuthorID: author.ID, Content: "activity rollback", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DROP TRIGGER IF EXISTS trg_test_outbox_failure ON outbox_events").Error
		_ = db.Exec("DROP FUNCTION IF EXISTS reject_test_outbox_insert()").Error
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("post_id = ? OR user_id IN ?", article.ID, []uint{commenter.ID, follower.ID, followed.ID}).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id IN ?", []uint{commenter.ID, follower.ID, followed.ID}).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", []uint{follower.ID, followed.ID}, []uint{follower.ID, followed.ID}).Delete(&models.UserFollow{})
		db.Unscoped().Delete(&article)
		db.Unscoped().Where("id IN ?", []uint{author.ID, commenter.ID, follower.ID, followed.ID}).Delete(&models.User{})
	})

	if err := db.Exec(`
CREATE OR REPLACE FUNCTION reject_test_outbox_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'test outbox insert failure';
END;
$$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER trg_test_outbox_failure BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_test_outbox_insert()`).Error; err != nil {
		t.Fatal(err)
	}

	var reply models.Post
	if err := persistPostGraph(&reply, commenter.ID, "should rollback", createPostRequest{
		Content:       "should rollback",
		ReplyToPostID: &article.ID,
	}, nil, now); err == nil {
		t.Fatal("comment unexpectedly succeeded with failing Outbox insert")
	}
	var replyCount int64
	if err := db.Model(&models.Post{}).Where("reply_to_post_id = ?", article.ID).Count(&replyCount).Error; err != nil {
		t.Fatal(err)
	}
	if replyCount != 0 {
		t.Fatalf("comment rows=%d want=0", replyCount)
	}
	var storedArticle models.Post
	if err := db.First(&storedArticle, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedArticle.ReplyCount != 0 {
		t.Fatalf("comment_count=%d want=0", storedArticle.ReplyCount)
	}

	if _, err := followAndLoadStateFromDB(follower.ID, followed.ID); err == nil {
		t.Fatal("follow unexpectedly succeeded with failing Outbox insert")
	}
	var followCount int64
	if err := db.Model(&models.UserFollow{}).Where("follower_id = ? AND following_id = ?", follower.ID, followed.ID).Count(&followCount).Error; err != nil {
		t.Fatal(err)
	}
	if followCount != 0 {
		t.Fatalf("follow rows=%d want=0", followCount)
	}

	if err := db.Exec("DROP TRIGGER trg_test_outbox_failure ON outbox_events").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP FUNCTION reject_test_outbox_insert()").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatal(fmt.Errorf("database unusable after rollback test: %w", err))
	}
}
