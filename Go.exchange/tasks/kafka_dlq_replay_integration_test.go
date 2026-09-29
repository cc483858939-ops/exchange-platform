package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/dlq"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLikeSnapshotDLQReplayDuplicateAndStaleSafetyIntegration(t *testing.T) {
	db := openKafkaDLQReplayIntegrationDatabase(t, &models.User{}, &models.Post{}, &models.ConsumerInbox{})
	groupID := "like-snapshot-dlq-replay-" + uuid.NewString()
	applicationConfig := replayIntegrationKafkaConfig()
	applicationConfig.LikeSnapshotGroupID = groupID
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: applicationConfig}
	t.Cleanup(func() { config.AppConfig = originalConfig })

	user := models.User{Username: "like-snapshot-dlq-replay-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "DLQ replay safety", Visibility: "public", LikeCount: 50, LikeSyncVersion: 10}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{}).Error
		_ = db.Unscoped().Delete(&models.Post{}, post.ID).Error
		_ = db.Unscoped().Delete(&models.User{}, user.ID).Error
	})

	now := time.Now().UTC()
	newer, err := eventing.NewLikeSnapshotEnvelope(post.ID, 55, 11)
	if err != nil {
		t.Fatal(err)
	}
	newerSource := envelopeKafkaMessage(t, applicationConfig.LikeSnapshotTopic, newer, []byte("post:"+uintString(post.ID)), now)
	newerReplays := []kafka.Message{
		replayIntegrationSource(t, applicationConfig, kafkaConsumerLikeSnapshotProjection, newerSource),
		replayIntegrationSource(t, applicationConfig, kafkaConsumerLikeSnapshotProjection, newerSource),
	}
	for _, replayed := range newerReplays {
		if err := processLikeSnapshotMessage(context.Background(), db, nil, applicationConfig, replayed); err != nil {
			t.Fatalf("duplicate replay: %v", err)
		}
	}

	stale, err := eventing.NewLikeSnapshotEnvelope(post.ID, 999, 9)
	if err != nil {
		t.Fatal(err)
	}
	staleMessage := replayIntegrationSource(t, applicationConfig, kafkaConsumerLikeSnapshotProjection, envelopeKafkaMessage(t, applicationConfig.LikeSnapshotTopic, stale, []byte("post:"+uintString(post.ID)), now.Add(time.Second)))
	if err := processLikeSnapshotMessage(context.Background(), db, nil, applicationConfig, staleMessage); err != nil {
		t.Fatalf("stale replay: %v", err)
	}

	assertLikeSnapshotPostState(t, db, post.ID, 55, 11)
	for _, eventID := range []string{newer.ID, stale.ID} {
		if count := countRecoveryInboxEvent(t, db, groupID, eventID); count != 1 {
			t.Fatalf("inbox rows for %s=%d want=1", eventID, count)
		}
	}
}

func TestPostEmbeddingDLQReplaySkipsCurrentProjectionIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	_, post := newPostEmbeddingIntegrationFixture(t, db, "embedding already reconciled")
	now := time.Now().UTC()
	stored := recoveryIntegrationEmbedding(post.ID, "v1", post.Content, []float32{1, 2}, now)
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}

	applicationConfig := replayIntegrationKafkaConfig()
	envelope, err := eventing.NewPostEmbeddingRequestedEnvelope(uuid.NewString(), post.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	source := envelopeKafkaMessage(t, applicationConfig.PostEmbeddingTopic, envelope, []byte("post:"+uintString(post.ID)), now)
	embedder := &postEmbeddingTestEmbedder{}
	for range 2 {
		replayed := replayIntegrationSource(t, applicationConfig, kafkaConsumerPostEmbedding, source)
		if err := processPostEmbeddingMessage(context.Background(), replayed, nil, embedder, gormPostEmbeddingStore{db: db}, "v1", applicationConfig, kafkaRetryPolicy{MaxAttempts: 1}); err != nil {
			t.Fatalf("current embedding replay: %v", err)
		}
	}
	if embedder.calls != 0 {
		t.Fatalf("embedder called %d times for current projection, want 0", embedder.calls)
	}
	persisted, err := (gormPostEmbeddingStore{db: db}).GetEmbedding(context.Background(), post.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ContentHash != embeddings.PostEmbeddingContentHash(post.Content) || persisted.Embedding.Slice()[0] != 1 || persisted.Embedding.Slice()[1] != 2 {
		t.Fatalf("replay changed current embedding: %+v", persisted)
	}
}

func TestNotificationDLQReplayDedupesAndMalformedUsesUnifiedTopicIntegration(t *testing.T) {
	db := openKafkaDLQReplayIntegrationDatabase(t, &models.User{}, &models.Notification{}, &models.ConsumerInbox{})
	if err := initialize.RunMigrationsWithDB(context.Background(), db); err != nil {
		t.Fatalf("initialize notification integration schema: %v", err)
	}
	applicationConfig := replayIntegrationKafkaConfig()
	applicationConfig.NotificationGroupID = "notification-dlq-replay-" + uuid.NewString()
	originalConfig, originalWorkerDB := config.AppConfig, global.WorkerDb
	config.AppConfig = &config.Config{Kafka: applicationConfig}
	global.WorkerDb = db
	t.Cleanup(func() { config.AppConfig, global.WorkerDb = originalConfig, originalWorkerDB })

	actor := models.User{Username: "notification-dlq-actor-" + uuid.NewString(), Password: "test"}
	recipient := models.User{Username: "notification-dlq-recipient-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("recipient_id = ?", recipient.ID).Delete(&models.Notification{}).Error
		_ = db.Where("consumer_name = ?", applicationConfig.NotificationGroupID).Delete(&models.ConsumerInbox{}).Error
		_ = db.Unscoped().Delete(&models.User{}, actor.ID).Error
		_ = db.Unscoped().Delete(&models.User{}, recipient.ID).Error
	})

	baseAt := time.Now().UTC()
	newFollow := notificationFollowEnvelope(t, uuid.NewString(), 120, actor.ID, recipient.ID, baseAt)
	newerSource := envelopeKafkaMessage(t, applicationConfig.ActivityEventsTopic, newFollow, nil, baseAt)
	newerReplays := []kafka.Message{
		replayIntegrationSource(t, applicationConfig, kafkaConsumerNotificationProjection, newerSource),
		replayIntegrationSource(t, applicationConfig, kafkaConsumerNotificationProjection, newerSource),
	}
	for _, replayed := range newerReplays {
		if err := processNotificationBatch(context.Background(), []kafka.Message{replayed}, &fakeRawKafkaMessagePublisher{}); err != nil {
			t.Fatalf("duplicate notification replay: %v", err)
		}
	}

	oldFollow := notificationFollowEnvelope(t, uuid.NewString(), 119, actor.ID, recipient.ID, baseAt.Add(-time.Minute))
	olderReplay := replayIntegrationSource(t, applicationConfig, kafkaConsumerNotificationProjection,
		envelopeKafkaMessage(t, applicationConfig.ActivityEventsTopic, oldFollow, nil, baseAt.Add(-time.Minute)))
	if err := processNotificationBatch(context.Background(), []kafka.Message{olderReplay}, &fakeRawKafkaMessagePublisher{}); err != nil {
		t.Fatalf("stale notification replay: %v", err)
	}

	var notifications []models.Notification
	if err := db.Where("dedupe_key = ?", "user_followed:"+uintString(actor.ID)+":"+uintString(recipient.ID)).Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].SourceVersion != 120 {
		t.Fatalf("notification state=%+v want one row at source version 120", notifications)
	}
	for _, eventID := range []string{newFollow.ID, oldFollow.ID} {
		if count := countRecoveryInboxEvent(t, db, applicationConfig.NotificationGroupID, eventID); count != 1 {
			t.Fatalf("notification inbox rows for %s=%d want=1", eventID, count)
		}
	}

	malformed := kafka.Message{Topic: applicationConfig.ActivityEventsTopic, Partition: 4, Offset: 91, Key: []byte{0xff}, Value: []byte{0xff, 0x00}}
	publisher := &fakeRawKafkaMessagePublisher{}
	if err := processNotificationBatch(context.Background(), []kafka.Message{malformed}, publisher); err != nil {
		t.Fatalf("malformed notification DLQ: %v", err)
	}
	if len(publisher.topics) != 1 || publisher.topics[0] != applicationConfig.ConsumerDLQTopic || len(publisher.messages) != 1 {
		t.Fatalf("malformed notification published topics=%v messages=%d", publisher.topics, len(publisher.messages))
	}
	deadLetter, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if deadLetter.Consumer != kafkaConsumerNotificationProjection || deadLetter.Source.Topic != applicationConfig.ActivityEventsTopic || deadLetter.Failure.Code != kafkaFailureCodeDecodeEnvelope {
		t.Fatalf("unified notification dead letter=%+v", deadLetter)
	}
}

func replayIntegrationKafkaConfig() config.KafkaConfig {
	return config.KafkaConfig{
		UserBehaviorTopic:         "goexchange.user.behavior.v1",
		LikeSnapshotTopic:         "goexchange.post.like.snapshot.v1",
		RecommendationEventsTopic: "goexchange.recommendation.events.v1",
		PostEmbeddingTopic:        "goexchange.post.embedding.v1",
		ActivityEventsTopic:       "goexchange.activity.events.v1",
		ConsumerDLQTopic:          "goexchange.consumer.dlq.v1",
	}
}

func openKafkaDLQReplayIntegrationDatabase(t *testing.T, modelsToMigrate ...interface{}) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func replayIntegrationSource(t *testing.T, applicationConfig config.KafkaConfig, consumer string, source kafka.Message) kafka.Message {
	t.Helper()
	eventID := ""
	if envelope, err := eventing.DecodeEnvelope(source.Value); err == nil {
		eventID = envelope.ID
	}
	failedAt := time.Now().UTC()
	record, err := eventing.NewDeadLetterRecord(consumer, eventID, source, eventing.DeadLetterFailure{
		Class: "permanent", Code: "integration_replay", Reason: "integration replay fixture", Attempts: 1, FailedAt: failedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	located := dlq.LocatedRecord{DLQTopic: applicationConfig.ConsumerDLQTopic, DLQPartition: 1, DLQOffset: 17, Record: record}
	registry, err := dlq.NewReplayPolicyRegistry(applicationConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dlq.BuildReplayPlan(located, registry); err != nil {
		t.Fatal(err)
	}
	replayed, err := dlq.BuildReplayMessage(located, uuid.NewString(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return replayed
}

func envelopeKafkaMessage(t *testing.T, topic string, envelope eventing.Envelope, key []byte, at time.Time) kafka.Message {
	t.Helper()
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: topic, Partition: 2, Offset: 71, Key: key, Value: value, Time: at}
}

func notificationFollowEnvelope(t *testing.T, eventID string, followID, actorID, recipientID uint, at time.Time) eventing.Envelope {
	t.Helper()
	envelope, err := eventing.NewUserFollowCreatedEnvelope(eventID, eventing.UserFollowCreatedPayload{
		FollowID: followID, FollowerID: actorID, FollowingID: recipientID, CreatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func uintString(value uint) string { return fmt.Sprintf("%d", value) }
