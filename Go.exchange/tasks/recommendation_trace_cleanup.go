package tasks

import (
	"context"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"
)

func startRecommendationTraceCleanup(ctx context.Context, wg *sync.WaitGroup) {
	cfg := recommendationTraceCleanupConfig()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.CleanupIntervalHours) * time.Hour)
		defer ticker.Stop()
		for {
			if err := cleanupRecommendationTraceOnce(ctx, time.Now().UTC(), cfg.RequestRetentionDays, cfg.CleanupBatchSize); err != nil {
				log.Printf("[RecommendationTraceCleanup] %v", err)
				metrics.RecordRecommendationTraceCleanupFailure()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func recommendationTraceCleanupConfig() config.RecommendationTraceConfig {
	cfg := config.RecommendationTraceConfig{ResultRetentionDays: 30, RequestRetentionDays: 90, CleanupIntervalHours: 6, CleanupBatchSize: 5000}
	if config.AppConfig == nil {
		return cfg
	}
	set := config.AppConfig.Recommendation.Trace
	if set.ResultRetentionDays > 0 {
		cfg.ResultRetentionDays = set.ResultRetentionDays
	}
	if set.RequestRetentionDays >= cfg.ResultRetentionDays {
		cfg.RequestRetentionDays = set.RequestRetentionDays
	}
	if set.CleanupIntervalHours > 0 {
		cfg.CleanupIntervalHours = set.CleanupIntervalHours
	}
	if set.CleanupBatchSize > 0 {
		cfg.CleanupBatchSize = set.CleanupBatchSize
	}
	return cfg
}

func cleanupRecommendationTraceOnce(ctx context.Context, now time.Time, requestRetentionDays, batchSize int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if global.Db == nil {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 5000
	}
	var rowsCleaned int64
	expiredSQL := "WITH doomed AS (SELECT request_id, position FROM recommendation_result_traces WHERE expires_at <= ? ORDER BY expires_at ASC LIMIT ?) DELETE FROM recommendation_result_traces AS trace USING doomed WHERE trace.request_id = doomed.request_id AND trace.position = doomed.position"
	expired := global.Db.WithContext(ctx).Exec(expiredSQL, now, batchSize)
	if expired.Error != nil {
		return expired.Error
	}
	rowsCleaned += expired.RowsAffected
	if err := ctx.Err(); err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -requestRetentionDays)
	requestSQL := "WITH doomed AS (SELECT request_id FROM recommendation_requests WHERE created_at < ? ORDER BY created_at ASC LIMIT ?) DELETE FROM recommendation_requests AS request USING doomed WHERE request.request_id = doomed.request_id"
	oldRequests := global.Db.WithContext(ctx).Exec(requestSQL, cutoff, batchSize)
	if oldRequests.Error != nil {
		return oldRequests.Error
	}
	rowsCleaned += oldRequests.RowsAffected
	metrics.AddRecommendationTraceCleanupRows(int(rowsCleaned))
	return nil
}
