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

func TestRecommendationMetricsProjectionIsIdempotentAndDimensionAwareIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.ConsumerInbox{},
		&models.RecommendationDailyMetric{},
		&models.ArticleBehavior{},
		&models.UserRecoProfileDirty{},
	); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	groupID := "test-rec-metrics-" + uuid.NewString()
	user := models.User{
		Username: "test-rec-metrics-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	userID := user.ID
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.RecommendationMetricsGroupID = groupID
	global.Db = db

	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Where("strategy_id LIKE ?", groupID+"%").Delete(&models.RecommendationDailyMetric{})
		db.Unscoped().Where("user_id = ?", userID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ?", userID).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("id = ?", userID).Delete(&models.User{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	baseAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: userID, ArticleID: 11, RequestID: uuid.NewString(),
		Scene: "recommendation_page", Position: 1,
		RankerVersion: "embedding_v1", RankerConfigHash: "0123456789ab",
		StrategyID: groupID, ReceivedAt: baseAt,
	}

	messages := make([]kafka.Message, 0, 105)
	for index := 0; index < 100; index++ {
		event, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationImpression, baseAt, base)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, recommendationMessage(t, event))
	}
	messages = append(messages, messages[0])
	messages = append(messages, kafka.Message{Value: []byte("{malformed")})

	clickPayload := base
	click, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationClick, baseAt, clickPayload)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, recommendationMessage(t, click))

	variantPosition := base
	variantPosition.Position = 2
	positionEvent, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationImpression, baseAt, variantPosition)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, recommendationMessage(t, positionEvent))

	variantStrategy := base
	variantStrategy.StrategyID = groupID + "-strategy-b"
	strategyEvent, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationImpression, baseAt, variantStrategy)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, recommendationMessage(t, strategyEvent))

	variantDate := base
	variantDate.ReceivedAt = baseAt.AddDate(0, 0, 1)
	dateEvent, err := eventing.NewRecommendationBehaviorEnvelope(uuid.NewString(), eventing.EventTypeRecommendationImpression, baseAt.AddDate(0, 0, 1), variantDate)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, recommendationMessage(t, dateEvent))

	if err := applyRecommendationMetricsBatch(messages); err != nil {
		t.Fatal(err)
	}

	var dirty models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", userID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != 1 || dirty.Reason != "recommendation_feedback_projection" {
		t.Fatalf("dirty profile=%#v want version=1 reason=%q", dirty, "recommendation_feedback_projection")
	}

	if err := applyRecommendationMetricsBatch([]kafka.Message{messages[0]}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("user_id = ?", userID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != 1 {
		t.Fatalf("replayed impression advanced dirty version to %d", dirty.DirtyVersion)
	}

	var metric models.RecommendationDailyMetric
	if err := db.Where("strategy_id = ? AND position = ? AND article_id = ?", groupID, 1, 11).First(&metric).Error; err != nil {
		t.Fatal(err)
	}
	if metric.ImpressionCount != 100 || metric.ClickCount != 1 {
		t.Fatalf("base metric=%#v want impressions=100 clicks=1", metric)
	}

	var metricRows int64
	if err := db.Model(&models.RecommendationDailyMetric{}).Where("strategy_id LIKE ?", groupID+"%").Count(&metricRows).Error; err != nil {
		t.Fatal(err)
	}
	if metricRows != 4 {
		t.Fatalf("metric rows=%d want=4 for position/strategy/date dimensions", metricRows)
	}

	var clickBehavior models.ArticleBehavior
	if err := db.Where("user_id = ? AND article_id = ? AND action = ?", userID, 11, eventing.RecommendationBehaviorActionClick).First(&clickBehavior).Error; err != nil {
		t.Fatal(err)
	}
	if clickBehavior.Count != 1 {
		t.Fatalf("derived click behavior=%#v", clickBehavior)
	}
}

func recommendationMessage(t *testing.T, event eventing.Envelope) kafka.Message {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: body}
}
