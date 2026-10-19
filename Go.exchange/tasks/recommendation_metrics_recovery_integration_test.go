package tasks

import (
	"context"
	"encoding/json"
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

// End-to-end consumer regression: a poison request_id goes to the existing
// DLQ path while a valid event in the same Kafka batch still commits to SQL.
func TestRecommendationMetricsInvalidRequestIDDLQAndContinueIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Post{}, &models.ConsumerInbox{},
		&models.RecommendationDailyMetric{}, &models.RecommendationCTRClaim{},
		&models.PostBehavior{}, &models.UserRecoProfileDirty{},
	); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "reco-invalid-request-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "invalid request DLQ integration", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	cfg := recommendationMetricsRecoveryConfig()
	cfg.RecommendationMetricsGroupID = "invalid-reco-id-" + uuid.NewString()
	strategy := "invalid-reco-" + uuid.NewString()
	t.Cleanup(func() {
		db.Where("consumer_name = ?", cfg.RecommendationMetricsGroupID).Delete(&models.ConsumerInbox{})
		db.Where("user_id = ?", user.ID).Delete(&models.RecommendationCTRClaim{})
		db.Unscoped().Where("strategy_id = ?", strategy).Delete(&models.RecommendationDailyMetric{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ? OR post_id = ?", user.ID, post.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Delete(&post)
		db.Unscoped().Delete(&user)
	})

	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	payload := eventing.RecommendationBehaviorPayload{
		UserID: user.ID, PostID: post.ID, RequestID: uuid.NewString(),
		Scene: "recommendation_page", Position: 1, RankerVersion: "ranker_v1",
		RankerConfigHash: "hash-invalid-req", StrategyID: strategy,
		ReceivedAt: at, SelectionMode: eventing.RecommendationSelectionModeRanked,
	}
	validEvent, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationImpression, at, payload)
	if err != nil {
		t.Fatal(err)
	}
	badEvent := validEvent
	badEvent.ID = uuid.NewString()
	payload.RequestID = "invalid-id"
	badEvent.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bad := mustRecommendationRecoveryMessageRaw(t, 300, badEvent)
	valid := mustRecommendationRecoveryMessageRaw(t, 301, validEvent)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, valid}}
	publisher := &fakeRawKafkaMessagePublisher{}
	err = consumeRecommendationMetricsMessagesWithApply(
		context.Background(), reader, publisher, db, cfg,
		kafkaRetryPolicy{MaxAttempts: 1}, applyRecommendationMetricRecords,
	)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consumer should proceed past invalid request UUID: %v", err)
	}
	if len(publisher.messages) != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 2 {
		t.Fatalf("DLQ=%d committed batches=%#v want one DLQ and both Kafka offsets committed", len(publisher.messages), reader.commits)
	}
	var dlq eventing.DeadLetterRecord
	if err := json.Unmarshal(publisher.messages[0].Value, &dlq); err != nil {
		t.Fatal(err)
	}
	if publisher.topics[0] != cfg.ConsumerDLQTopic || dlq.Consumer != kafkaConsumerRecommendationMetrics ||
		dlq.Source.Offset != 300 || dlq.Failure.Code != kafkaFailureCodeInvalidPayload {
		t.Fatalf("unexpected invalid UUID DLQ: topic=%q record=%+v", publisher.topics[0], dlq)
	}

	var metricsRow models.RecommendationDailyMetric
	if err := db.Where("strategy_id = ?", strategy).First(&metricsRow).Error; err != nil {
		t.Fatal(err)
	}
	if metricsRow.ImpressionCount != 1 || metricsRow.ClickCount != 0 {
		t.Fatalf("valid event projection=%#v want one impression", metricsRow)
	}
	var claims int64
	if err := db.Model(&models.RecommendationCTRClaim{}).Where("user_id = ?", user.ID).Count(&claims).Error; err != nil {
		t.Fatal(err)
	}
	if claims != 1 {
		t.Fatalf("CTR claims=%d want one valid opportunity only", claims)
	}
	if got := countRecoveryInboxEvent(t, db, cfg.RecommendationMetricsGroupID, validEvent.ID); got != 1 {
		t.Fatalf("valid event inbox count=%d want=1", got)
	}
	if got := countRecoveryInboxEvent(t, db, cfg.RecommendationMetricsGroupID, badEvent.ID); got != 0 {
		t.Fatalf("invalid event unexpectedly entered inbox: %d", got)
	}
}

func TestRecommendationMetricsProjectionRecoverySemanticsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.RecommendationDailyMetric{},
		&models.RecommendationCTRClaim{},
		&models.PostBehavior{}, &models.UserRecoProfileDirty{},
	); err != nil {
		t.Fatal(err)
	}

	consumerName := "test-recommendation-metrics-recovery-" + uuid.NewString()
	strategy := "strategy-recovery-" + uuid.NewString()
	kafkaConfig := config.KafkaConfig{RecommendationMetricsGroupID: consumerName}
	user := models.User{Username: "recommendation-recovery-user-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "recommendation recovery", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("consumer_name = ?", consumerName).Delete(&models.ConsumerInbox{})
		db.Where("user_id = ?", user.ID).Delete(&models.RecommendationCTRClaim{})
		db.Unscoped().Where("strategy_id LIKE ?", strategy+"%").Delete(&models.RecommendationDailyMetric{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ? OR post_id = ?", user.ID, post.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Delete(&post)
		db.Unscoped().Delete(&user)
	})

	baseAt := time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)
	baseRequestID := uuid.NewString()
	newRecord := func(eventID, eventType, strategyID string) recommendationMetricEvent {
		t.Helper()
		requestID := baseRequestID
		if strategyID != strategy {
			requestID = uuid.NewString()
		}
		payload := eventing.RecommendationBehaviorPayload{
			UserID: user.ID, PostID: post.ID, RequestID: requestID, Scene: "recommendation_page", Position: 1,
			RankerVersion: "ranker_v1", RankerConfigHash: "hash-recovery", StrategyID: strategyID, ReceivedAt: baseAt,
			SelectionMode: eventing.RecommendationSelectionModeRanked,
		}
		event, err := eventing.NewRecommendationBehaviorEnvelope(eventID, eventType, baseAt, payload)
		if err != nil {
			t.Fatal(err)
		}
		value := recommendationMessage(t, event)
		record, err := decodeRecommendationMetricEvent(value)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	impression := newRecord(uuid.NewString(), eventing.EventTypeRecommendationImpression, strategy)
	click := newRecord(uuid.NewString(), eventing.EventTypeRecommendationClick, strategy)
	for _, record := range []recommendationMetricEvent{impression, click} {
		if err := applyRecommendationMetricRecords(context.Background(), db, consumerName, kafkaConfig, []recommendationMetricEvent{record}); err != nil {
			t.Fatal(err)
		}
	}
	var dirty models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", user.ID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	firstDirtyVersion := dirty.DirtyVersion
	for _, record := range []recommendationMetricEvent{impression, click} {
		if err := applyRecommendationMetricRecords(context.Background(), db, consumerName, kafkaConfig, []recommendationMetricEvent{record}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("user_id = ?", user.ID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != firstDirtyVersion {
		t.Fatalf("duplicate deliveries advanced profile dirty version from %d to %d", firstDirtyVersion, dirty.DirtyVersion)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, impression.Envelope.ID); count != 1 {
		t.Fatalf("impression inbox rows=%d want=1", count)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, click.Envelope.ID); count != 1 {
		t.Fatalf("click inbox rows=%d want=1", count)
	}
	var metric models.RecommendationDailyMetric
	if err := db.Where("strategy_id = ?", strategy).First(&metric).Error; err != nil {
		t.Fatal(err)
	}
	if metric.ImpressionCount != 1 || metric.ClickCount != 1 {
		t.Fatalf("metric=%#v want impression=1 click=1", metric)
	}
	var clickBehavior models.PostBehavior
	if err := db.Where("user_id = ? AND post_id = ? AND action = ?", user.ID, post.ID, eventing.RecommendationBehaviorActionClick).First(&clickBehavior).Error; err != nil {
		t.Fatal(err)
	}
	if clickBehavior.Count != 1 {
		t.Fatalf("derived click behavior count=%d want=1", clickBehavior.Count)
	}

	rollback := newRecord(uuid.NewString(), eventing.EventTypeRecommendationImpression, strategy+"-rollback")
	dropTrigger := installKafkaRecoveryInsertFailureTrigger(t, db, "recommendation_daily_metrics", fmt.Sprintf("NEW.strategy_id = '%s'", strategy+"-rollback"))
	err = applyRecommendationMetricRecords(context.Background(), db, consumerName, kafkaConfig, []recommendationMetricEvent{rollback})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
		t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, rollback.Envelope.ID); count != 0 {
		t.Fatalf("failed projection left inbox rows=%d want=0", count)
	}
	var rollbackMetricCount int64
	if err := db.Model(&models.RecommendationDailyMetric{}).Where("strategy_id = ?", strategy+"-rollback").Count(&rollbackMetricCount).Error; err != nil {
		t.Fatal(err)
	}
	if rollbackMetricCount != 0 {
		t.Fatalf("failed projection left metric rows=%d want=0", rollbackMetricCount)
	}
	if err := db.Where("user_id = ?", user.ID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != firstDirtyVersion {
		t.Fatalf("failed transaction advanced profile dirty version to %d want %d", dirty.DirtyVersion, firstDirtyVersion)
	}
	dropTrigger()
	if err := applyRecommendationMetricRecords(context.Background(), db, consumerName, kafkaConfig, []recommendationMetricEvent{rollback}); err != nil {
		t.Fatalf("redelivery after trigger removal: %v", err)
	}
	if count := countRecoveryInboxEvent(t, db, consumerName, rollback.Envelope.ID); count != 1 {
		t.Fatalf("recovered inbox rows=%d want=1", count)
	}
	if err := db.Model(&models.RecommendationDailyMetric{}).Where("strategy_id = ?", strategy+"-rollback").Count(&rollbackMetricCount).Error; err != nil {
		t.Fatal(err)
	}
	if rollbackMetricCount != 1 {
		t.Fatalf("recovered metric rows=%d want=1", rollbackMetricCount)
	}
}
