package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLikeSnapshotProjectionIntegrationRecoverySemantics(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.ConsumerInbox{}); err != nil {
		t.Fatal(err)
	}

	groupID := "like-snapshot-recovery-" + uuid.NewString()
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{LikeSnapshotGroupID: groupID}}
	t.Cleanup(func() { config.AppConfig = originalConfig })

	user := models.User{Username: "like-snapshot-recovery-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "snapshot recovery fixture", Visibility: "public", LikeCount: 50, LikeSyncVersion: 10}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Unscoped().Delete(&models.Post{}, post.ID).Error
		_ = db.Unscoped().Delete(&models.User{}, user.ID).Error
	})

	stale, err := eventing.NewLikeSnapshotEnvelope(post.ID, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := applyLikeSnapshotEvent(context.Background(), db, stale)
	if err != nil || outcome != likeSnapshotNoop {
		t.Fatalf("stale snapshot outcome=%d err=%v want noop", outcome, err)
	}
	assertLikeSnapshotPostState(t, db, post.ID, 50, 10)

	newer, err := eventing.NewLikeSnapshotEnvelope(post.ID, 55, 11)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err = applyLikeSnapshotEvent(context.Background(), db, newer)
	if err != nil || outcome != likeSnapshotApplied {
		t.Fatalf("newer snapshot outcome=%d err=%v want applied", outcome, err)
	}
	assertLikeSnapshotPostState(t, db, post.ID, 55, 11)

	duplicate := newer
	duplicate.Payload, err = json.Marshal(eventing.PostLikeSnapshotPayload{PostID: post.ID, LikeCount: 999, Version: 11})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err = applyLikeSnapshotEvent(context.Background(), db, duplicate)
	if err != nil || outcome != likeSnapshotNoop {
		t.Fatalf("duplicate snapshot outcome=%d err=%v want noop", outcome, err)
	}
	assertLikeSnapshotPostState(t, db, post.ID, 55, 11)

	functionName := "fail_like_snapshot_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	triggerName := functionName + "_trigger"
	if err := db.Exec(fmt.Sprintf(`CREATE FUNCTION public."%s"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test snapshot projection failure'; END; $$`, functionName)).Error; err != nil {
		t.Fatalf("create failure trigger function: %v", err)
	}
	if err := db.Exec(fmt.Sprintf(`CREATE TRIGGER "%s" BEFORE UPDATE ON posts FOR EACH ROW WHEN (OLD.id = %d) EXECUTE FUNCTION public."%s"()`, triggerName, post.ID, functionName)).Error; err != nil {
		_ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS public."%s"()`, functionName)).Error
		t.Fatalf("create failure trigger: %v", err)
	}
	dropFailureTrigger := func() {
		_ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS "%s" ON posts`, triggerName)).Error
		_ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS public."%s"()`, functionName)).Error
	}
	t.Cleanup(dropFailureTrigger)

	rollbackEvent, err := eventing.NewLikeSnapshotEnvelope(post.ID, 60, 12)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err = applyLikeSnapshotEvent(context.Background(), db, rollbackEvent)
	if err == nil || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
		t.Fatalf("trigger failure outcome=%d class=%q code=%q err=%v", outcome, kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	var inboxRows int64
	if err := db.Model(&models.ConsumerInbox{}).Where("consumer_name = ? AND event_id = ?", groupID, rollbackEvent.ID).Count(&inboxRows).Error; err != nil {
		t.Fatal(err)
	}
	if inboxRows != 0 {
		t.Fatalf("inbox rows=%d after failed projection want 0", inboxRows)
	}
	dropFailureTrigger()

	rollbackBytes, err := json.Marshal(rollbackEvent)
	if err != nil {
		t.Fatal(err)
	}
	redelivery := kafka.Message{Topic: "goexchange.post.like.snapshot.v1", Partition: 1, Offset: 200, Key: []byte(fmt.Sprintf("post:%d", post.ID)), Value: rollbackBytes}
	commitFailure := errors.New("source commit failed")
	failedCommitReader := &fakeLikeSnapshotReader{messages: []kafka.Message{redelivery}, commitErr: commitFailure}
	publisher := &fakeRawKafkaMessagePublisher{}
	err = consumeLikeSnapshotMessages(context.Background(), failedCommitReader, publisher, db, likeSnapshotTestConfigWithGroup(groupID))
	if !errors.Is(err, commitFailure) || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit {
		t.Fatalf("failed source commit error=%v code=%q", err, kafkaFailureCode(err))
	}
	assertLikeSnapshotPostState(t, db, post.ID, 60, 12)

	replayReader := &fakeLikeSnapshotReader{messages: []kafka.Message{redelivery}, fetchErr: errFakeLikeSnapshotFetchDone}
	err = consumeLikeSnapshotMessages(context.Background(), replayReader, publisher, db, likeSnapshotTestConfigWithGroup(groupID))
	if !errors.Is(err, errFakeLikeSnapshotFetchDone) {
		t.Fatalf("redelivery consume error=%v", err)
	}
	if len(replayReader.commits) != 1 || replayReader.commits[0].Offset != redelivery.Offset {
		t.Fatalf("redelivery commits=%v want source offset %d", messageOffsets(replayReader.commits), redelivery.Offset)
	}
	assertLikeSnapshotPostState(t, db, post.ID, 60, 12)
	if len(publisher.messages) != 0 {
		t.Fatalf("unexpected DLQ messages=%d", len(publisher.messages))
	}
}

func likeSnapshotTestConfigWithGroup(groupID string) config.KafkaConfig {
	cfg := likeSnapshotTestConfig()
	cfg.LikeSnapshotGroupID = groupID
	return cfg
}

func assertLikeSnapshotPostState(t *testing.T, db *gorm.DB, postID uint, wantCount, wantVersion int64) {
	t.Helper()
	var got models.Post
	if err := db.First(&got, postID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LikeCount != wantCount || got.LikeSyncVersion != wantVersion {
		t.Fatalf("post like state count=%d version=%d want=%d/%d", got.LikeCount, got.LikeSyncVersion, wantCount, wantVersion)
	}
}
