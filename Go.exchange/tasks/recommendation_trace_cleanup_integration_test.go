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

	originalDB, originalWorkerDB, originalConfig := global.Db, global.WorkerDb, config.AppConfig
	global.Db = db
	global.WorkerDb = db
	config.AppConfig = &config.Config{
		Recommendation: config.RecommendationConfig{
			Trace: config.RecommendationTraceConfig{
				ResultRetentionDays:           120,
				RequestRetentionDays:          90,
				CleanupIntervalSeconds:        600,
				CleanupCatchupIntervalSeconds: 60,
				CleanupResultBatchSize:        5,
				CleanupRequestBatchSize:       5,
				CleanupRunBudgetSeconds:       60,
				CleanupMaxResultRowsPerRun:    1000,
				CleanupMaxRequestRowsPerRun:   1000,
			},
		},
	}
	t.Cleanup(func() {
		global.Db, global.WorkerDb = originalDB, originalWorkerDB
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
	articleTwo := models.Post{
		AuthorID: user.ID, Content: "trace cleanup cascade body", Visibility: "public",
	}
	if err := db.Create(&articleTwo).Error; err != nil {
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
	recentRequests := make([]models.RecommendationRequest, 0, 11)
	oldRequests := []models.RecommendationRequest{requestC}
	for i := 0; i < 11; i++ {
		recentRequests = append(recentRequests, newRequest(now.AddDate(0, 0, -10)))
		oldRequests = append(oldRequests, newRequest(now.AddDate(0, 0, -121)))
	}
	requestRows := []models.RecommendationRequest{requestA, requestB}
	requestRows = append(requestRows, recentRequests...)
	requestRows = append(requestRows, oldRequests...)
	requestIDs := make([]string, 0, len(requestRows))
	for _, request := range requestRows {
		requestIDs = append(requestIDs, request.RequestID)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("id = ?", articleTwo.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	if err := db.Create(&requestRows).Error; err != nil {
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
	traceRows := []models.RecommendationResultTrace{traceA, traceB}
	for _, request := range recentRequests {
		trace := traceB
		trace.RequestID = request.RequestID
		trace.CreatedAt = request.CreatedAt
		traceRows = append(traceRows, trace)
	}
	for _, request := range oldRequests {
		firstTrace := traceA
		firstTrace.RequestID = request.RequestID
		firstTrace.Position = 1
		firstTrace.CreatedAt = request.CreatedAt
		secondTrace := firstTrace
		secondTrace.Position = 2
		secondTrace.PostID = articleTwo.ID
		traceRows = append(traceRows, firstTrace, secondTrace)
	}
	if err := db.Create(&traceRows).Error; err != nil {
		t.Fatal(err)
	}

	runResult, err := runRecommendationTraceCleanup(
		context.Background(),
		cfg,
		func() time.Time { return now },
		func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
			return deleteExpiredRecommendationResultTraceBatch(ctx, db, cutoff, limit)
		},
		func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
			return deleteOldRecommendationRequestBatch(ctx, db, cutoff, limit)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !runResult.CaughtUp || runResult.BudgetReached || runResult.Cycles != 3 || runResult.ResultRows != 12 || runResult.RequestRows != 12 {
		t.Fatalf("cleanup run result=%+v", runResult)
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

	var recentRequestCount int64
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id IN ?", requestIDsFor(recentRequests)).Count(&recentRequestCount).Error; err != nil {
		t.Fatal(err)
	}
	if recentRequestCount != int64(len(recentRequests)) {
		t.Fatalf("recent request count=%d, want %d", recentRequestCount, len(recentRequests))
	}
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id IN ?", requestIDsFor(recentRequests)).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 0 {
		t.Fatalf("expired multi-batch trace count=%d, want 0", traceCount)
	}
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id IN ?", requestIDsFor(oldRequests)).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestCount != 0 {
		t.Fatalf("expired request count=%d, want 0", requestCount)
	}
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id IN ?", requestIDsFor(oldRequests)).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 0 {
		t.Fatalf("cascaded child trace count=%d, want 0", traceCount)
	}

	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", requestC.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestCount != 0 {
		t.Fatalf("request C count=%d, want 0", requestCount)
	}
}

func requestIDsFor(requests []models.RecommendationRequest) []string {
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RequestID)
	}
	return ids
}
