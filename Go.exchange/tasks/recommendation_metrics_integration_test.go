package tasks

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationMetricsProjectionIsIdempotentIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ConsumerInbox{}, &models.RecommendationDailyMetric{}); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	groupID := "test-rec-metrics-" + uuid.NewString()
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.RecommendationMetricsGroupID = groupID
	global.Db = db
	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Where("strategy_id = ?", groupID).Delete(&models.RecommendationDailyMetric{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	occurredAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	payload := eventing.RecommendationEventsRecordedPayload{
		UserID: 7,
		Events: []eventing.RecommendationEventFact{{
			EventID: uuid.NewString(), UserID: 7, RequestID: uuid.NewString(), ArticleID: 11,
			EventType: models.RecommendationEventTypeImpression, Scene: "recommendation_page",
			Position: 1, RankerVersion: "rules_v1", RankerConfigHash: "0123456789ab",
			StrategyID: groupID, OccurredAt: occurredAt, ReceivedAt: occurredAt,
		}},
	}
	body, _ := json.Marshal(payload)
	event := eventing.Envelope{
		ID: uuid.NewString(), Type: eventing.EventTypeRecommendationEventsRecorded,
		SchemaVersion: 1, AggregateType: "recommendation-telemetry-batch",
		AggregateID: "7", OccurredAt: occurredAt, Payload: body,
	}
	if err := applyRecommendationMetricsEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationMetricsEvent(event); err != nil {
		t.Fatal(err)
	}

	var metric models.RecommendationDailyMetric
	if err := db.Where("strategy_id = ?", groupID).First(&metric).Error; err != nil {
		t.Fatal(err)
	}
	if metric.ImpressionCount != 1 || metric.ClickCount != 0 {
		t.Fatalf("metric=%#v", metric)
	}
}
