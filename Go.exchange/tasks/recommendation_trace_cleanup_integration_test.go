package tasks

import (
	"context"
	"os"
	"sync"
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

	originalWorkerDB, originalConfig := global.WorkerDb, config.AppConfig
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
		global.WorkerDb = originalWorkerDB
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

func TestRecommendationTraceCleanupAdvisoryLockIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL advisory-lock integration test")
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
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
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

	user := models.User{Username: "recommendation-trace-cleanup-lock-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "trace cleanup lock fixture", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := models.RecommendationRequest{
		RequestID:        uuid.NewString(),
		UserID:           user.ID,
		Scene:            "for_you",
		StrategyID:       "trace-cleanup-lock",
		RankerVersion:    "test",
		RankerConfigHash: "test",
		RequestedLimit:   1,
		CreatedAt:        now.AddDate(0, 0, -120),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	trace := models.RecommendationResultTrace{
		RequestID: request.RequestID,
		Position:  1,
		PostID:    post.ID,
		AuthorID:  user.ID,
		CreatedAt: now.AddDate(0, 0, -120),
		ExpiresAt: now.AddDate(0, 0, -120),
	}
	if err := db.Create(&trace).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupDB := db.WithContext(context.Background())
		cleanupDB.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationResultTrace{})
		cleanupDB.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationRequest{})
		cleanupDB.Unscoped().Where("id = ?", post.ID).Delete(&models.Post{})
		cleanupDB.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	cfg := testRecommendationTraceCleanupConfig()
	cfg.ResultRetentionDays = 30
	cfg.RequestRetentionDays = 90
	cleanupOnConnection := func(ctx context.Context, connDB *gorm.DB) (recommendationTraceCleanupRunResult, error) {
		return runRecommendationTraceCleanup(
			ctx,
			cfg,
			time.Now,
			func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteExpiredRecommendationResultTraceBatch(ctx, connDB, cutoff, limit)
			},
			func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteOldRecommendationRequestBatch(ctx, connDB, cutoff, limit)
			},
		)
	}
	withPinnedPID := func(pid *int) recommendationTraceCleanupPinnedConnection {
		return func(ctx context.Context, callback func(*gorm.DB) error) error {
			return db.WithContext(ctx).Connection(func(connDB *gorm.DB) error {
				if err := connDB.Raw("SELECT pg_backend_pid()").Scan(pid).Error; err != nil {
					return err
				}
				return callback(connDB)
			})
		}
	}
	withLock := func(ctx context.Context, pin recommendationTraceCleanupPinnedConnection, run func(*gorm.DB) (recommendationTraceCleanupRunResult, error)) (recommendationTraceCleanupRunResult, error) {
		return withRecommendationTraceCleanupLockUsing(
			ctx,
			pin,
			run,
			tryRecommendationTraceCleanupAdvisoryLock,
			unlockRecommendationTraceCleanupAdvisoryLock,
		)
	}

	type asyncRun struct {
		result recommendationTraceCleanupRunResult
		err    error
	}
	aCtx, cancelA := context.WithTimeout(context.Background(), 20*time.Second)
	releaseA := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseA) }) }
	aEntered := make(chan struct{})
	aDone := make(chan asyncRun, 1)
	aPID := 0
	aFinished := false
	go func() {
		result, runErr := withLock(aCtx, withPinnedPID(&aPID), func(connDB *gorm.DB) (recommendationTraceCleanupRunResult, error) {
			close(aEntered)
			select {
			case <-releaseA:
			case <-aCtx.Done():
				return recommendationTraceCleanupRunResult{}, aCtx.Err()
			}
			return cleanupOnConnection(aCtx, connDB)
		})
		aDone <- asyncRun{result: result, err: runErr}
	}()
	defer func() {
		release()
		cancelA()
		if !aFinished {
			select {
			case <-aDone:
			case <-time.After(5 * time.Second):
				t.Error("timed out waiting for first cleanup runner to stop")
			}
		}
	}()
	select {
	case <-aEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("first cleanup runner did not acquire the advisory lock")
	}

	bPID := 0
	bRunnerCalls := 0
	bRun := func(connDB *gorm.DB) (recommendationTraceCleanupRunResult, error) {
		bRunnerCalls++
		return cleanupOnConnection(context.Background(), connDB)
	}
	bResult, err := withLock(context.Background(), withPinnedPID(&bPID), bRun)
	if err != nil {
		t.Fatal(err)
	}
	if !bResult.LockSkipped || bResult.CaughtUp || bResult.Cycles != 0 || bResult.ResultRows != 0 || bResult.RequestRows != 0 {
		t.Fatalf("contending runner result=%+v", bResult)
	}
	if bRunnerCalls != 0 {
		t.Fatalf("contending runner executed cleanup %d times", bRunnerCalls)
	}
	if aPID == 0 || bPID == 0 || aPID == bPID {
		t.Fatalf("expected two distinct pinned PostgreSQL connections, first pid=%d second pid=%d", aPID, bPID)
	}
	var traceCount, requestCount int64
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id = ?", request.RequestID).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", request.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 1 || requestCount != 1 {
		t.Fatalf("losing runner mutated eligible rows: trace count=%d request count=%d", traceCount, requestCount)
	}

	release()
	select {
	case aRun := <-aDone:
		aFinished = true
		if aRun.err != nil {
			t.Fatal(aRun.err)
		}
		if !aRun.result.CaughtUp || aRun.result.ResultRows != 1 || aRun.result.RequestRows != 1 {
			t.Fatalf("first runner result=%+v", aRun.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first cleanup runner did not finish after release")
	}

	bResult, err = withLock(context.Background(), withPinnedPID(&bPID), bRun)
	if err != nil {
		t.Fatal(err)
	}
	if bResult.LockSkipped || !bResult.CaughtUp || bRunnerCalls != 1 {
		t.Fatalf("second runner result=%+v, runner calls=%d", bResult, bRunnerCalls)
	}
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id = ?", request.RequestID).Count(&traceCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", request.RequestID).Count(&requestCount).Error; err != nil {
		t.Fatal(err)
	}
	if traceCount != 0 || requestCount != 0 {
		t.Fatalf("first runner left eligible rows: trace count=%d request count=%d", traceCount, requestCount)
	}
}

func requestIDsFor(requests []models.RecommendationRequest) []string {
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RequestID)
	}
	return ids
}
