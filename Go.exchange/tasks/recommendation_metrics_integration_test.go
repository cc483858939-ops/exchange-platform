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
	"github.com/segmentio/kafka-go"
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
	if err := db.AutoMigrate(&models.ConsumerInbox{}, &models.RecommendationDailyMetric{}, &models.ArticleBehavior{}); err != nil {
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
		db.Where("user_id = ?", 7).Delete(&models.ArticleBehavior{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	occurredAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 11, RequestID: uuid.NewString(),
		Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v1", RankerConfigHash: "0123456789ab",
		StrategyID: groupID, ReceivedAt: occurredAt,
	}
	feedVisibleTimeMS := int64(3000)
	events := make([]eventing.Envelope, 0, 3)
	for _, item := range []struct {
		eventType string
		payload   eventing.RecommendationBehaviorPayload
	}{
		{eventing.EventTypeRecommendationImpression, base},
		{eventing.EventTypeRecommendationClick, base},
		{
			eventing.EventTypeRecommendationFeedDwell,
			func() eventing.RecommendationBehaviorPayload {
				payload := base
				payload.FeedVisibleTimeMS = &feedVisibleTimeMS
				return payload
			}(),
		},
	} {
		envelope, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), item.eventType, occurredAt, item.payload)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, envelope)
	}

	messages := make([]kafka.Message, 0, len(events))
	for _, event := range events {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, kafka.Message{Value: body})
	}
	if err := applyRecommendationMetricsBatch(messages); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationMetricsBatch(messages); err != nil {
		t.Fatal(err)
	}

	var metric models.RecommendationDailyMetric
	if err := db.Where("strategy_id = ?", groupID).First(&metric).Error; err != nil {
		t.Fatal(err)
	}
	if metric.ImpressionCount != 1 || metric.ClickCount != 1 ||
		metric.FeedDwellCount != 1 || metric.FeedVisibleTimeMS != 3000 {
		t.Fatalf("metric=%#v", metric)
	}

	var click models.ArticleBehavior
	if err := db.Where("user_id = ? AND article_id = ? AND action = ?", 7, 11, eventing.RecommendationBehaviorActionClick).First(&click).Error; err != nil {
		t.Fatal(err)
	}
	if click.Count != 1 {
		t.Fatalf("derived click behavior=%#v", click)
	}
}
