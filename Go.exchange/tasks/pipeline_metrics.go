package tasks

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"
)

func startPipelineMetrics(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			refreshPipelineMetrics()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func refreshPipelineMetrics() {
	if global.Db == nil {
		return
	}
	states := []string{
		models.ArticleAnalysisJobQueued,
		models.ArticleAnalysisJobLeased,
		models.ArticleAnalysisJobRetryWait,
		models.ArticleAnalysisJobSucceeded,
		models.ArticleAnalysisJobDead,
	}
	for _, state := range states {
		var count int64
		if err := global.Db.Model(&models.ArticleAnalysisJob{}).Where("state = ?", state).Count(&count).Error; err != nil {
			log.Printf("[Metrics] count article analysis state %s: %v", state, err)
			return
		}
		metrics.SetArticleAnalysisJobs(state, float64(count))
	}
	var pendingOutbox int64
	if err := global.Db.Model(&models.OutboxEvent{}).Where("published_at IS NULL").Count(&pendingOutbox).Error; err != nil {
		log.Printf("[Metrics] count pending outbox: %v", err)
		return
	}
	metrics.SetOutboxPending(float64(pendingOutbox))
	var oldestRecommendationOutbox sql.NullTime
	if err := global.Db.Model(&models.OutboxEvent{}).
		Select("MIN(created_at)").
		Where("published_at IS NULL AND event_type = ?", eventing.EventTypeRecommendationEventsRecorded).
		Scan(&oldestRecommendationOutbox).Error; err != nil {
		log.Printf("[Metrics] load oldest recommendation outbox: %v", err)
		return
	}
	oldestAge := 0.0
	if oldestRecommendationOutbox.Valid {
		oldestAge = time.Since(oldestRecommendationOutbox.Time).Seconds()
		if oldestAge < 0 {
			oldestAge = 0
		}
	}
	metrics.SetRecommendationTelemetryOutboxOldestAge(oldestAge)
	if global.RedisDB != nil {
		if dirty, err := global.RedisDB.SCard(likes.DirtyKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("dirty", float64(dirty))
		}
		if processing, err := global.RedisDB.ZCard(likes.ProcessingKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("processing", float64(processing))
		}
		if dirty, err := global.RedisDB.SCard(likes.BehaviorDirtyKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_dirty", float64(dirty))
		}
		if processing, err := global.RedisDB.ZCard(likes.BehaviorProcessingKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_processing", float64(processing))
		}
		if states, err := global.RedisDB.HLen(likes.BehaviorStateKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_state", float64(states))
		}

	}
}
