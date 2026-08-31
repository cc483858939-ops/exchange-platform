package tasks

import (
	"context"
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

func TestRecommendationTraceCleanupIntegration(t *testing.T) {
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
		&models.Post{},
		&models.RecommendationRequest{},
		&models.RecommendationResultTrace{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS fk_recommendation_result_traces_request").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_result_traces ADD CONSTRAINT fk_recommendation_result_traces_request FOREIGN KEY (request_id) REFERENCES recommendation_requests(request_id) ON UPDATE CASCADE ON DELETE CASCADE").Error; err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{
		Recommendation: config.RecommendationConfig{
			Trace: config.RecommendationTraceConfig{
				ResultRetentionDays:  120,
				RequestRetentionDays: 90,
				CleanupBatchSize:     5000,
			},
		},
	}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	cfg := recommendationTraceCleanupConfig()
	if cfg.ResultRetentionDays != 120 || cfg.RequestRetentionDays != 120 {
		t.Fatalf("effective retention result=%d request=%d", cfg.ResultRetentionDays, cfg.RequestRetentionDays)
	}

	user := models.User{
		Username: "recommendation-trace-cleanup-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{
		AuthorID: user.ID, Content: "trace cleanup body", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	newRequest := func(createdAt time.Time) models.RecommendationRequest {
		return models.RecommendationRequest{
			RequestID:        uuid.NewString(),
			UserID:           user.ID,
			Scene:            "for_you",
			StrategyID:       "trace-cleanup",
			RankerVersion:    "test",
			RankerConfigHash: "test",
			RequestedLimit:   1,
			CreatedAt:        createdAt,
		}
	}
	requestA := newRequest(now.AddDate(0, 0, -100))
	requestB := newRequest(now.AddDate(0, 0, -10))
	requestC := newRequest(now.AddDate(0, 0, -121))
	requestIDs := []string{requestA.RequestID, requestB.RequestID, requestC.RequestID}
	t.Cleanup(func() {
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	if err := db.Create(&[]models.RecommendationRequest{requestA, requestB, requestC}).Error; err != nil {
		t.Fatal(err)
	}

	traceA := models.RecommendationResultTrace{
		RequestID: requestA.RequestID,
		Position:  1,
		PostID:    article.ID,
		AuthorID:  user.ID,
		CreatedAt: now.AddDate(0, 0, -100),
		ExpiresAt: now.AddDate(0, 0, 20),
	}
	traceB := traceA
	traceB.RequestID = requestB.RequestID
	traceB.CreatedAt = now.AddDate(0, 0, -10)
	traceB.ExpiresAt = now.Add(-time.Hour)
	if err := db.Create(&traceA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&traceB).Error; err != nil {
		t.Fatal(err)
	}

	if err := cleanupRecommendationTraceOnce(context.Background(), now, cfg.RequestRetentionDays, cfg.CleanupBatchSize); err != nil {
		t.Fatal(err)
	}

	var requestCount int64
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", requestA.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 {
		t.Fatalf("request A count=%d, want 1", requestCount)
	}
	var traceCount int64
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id = ?", requestA.RequestID).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 1 {
		t.Fatalf("request A trace count=%d, want 1", traceCount)
	}

	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", requestB.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 {
		t.Fatalf("request B count=%d, want 1", requestCount)
	}
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id = ?", requestB.RequestID).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 0 {
		t.Fatalf("expired request B trace count=%d, want 0", traceCount)
	}

	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", requestC.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestCount != 0 {
		t.Fatalf("request C count=%d, want 0", requestCount)
	}
}
