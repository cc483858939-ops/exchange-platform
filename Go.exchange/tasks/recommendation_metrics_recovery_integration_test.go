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

func TestRecommendationMetricsProjectionRecoverySemanticsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.RecommendationDailyMetric{},
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
		db.Unscoped().Where("strategy_id LIKE ?", strategy+"%").Delete(&models.RecommendationDailyMetric{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ? OR post_id = ?", user.ID, post.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Delete(&post)
		db.Unscoped().Delete(&user)
	})

	baseAt := time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)
	newRecord := func(eventID, eventType, strategyID string) recommendationMetricEvent {
		t.Helper()
		payload := eventing.RecommendationBehaviorPayload{
			UserID: user.ID, PostID: post.ID, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
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
