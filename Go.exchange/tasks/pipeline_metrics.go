package tasks

import (
	"context"
	"log"
	"sync"
	"time"

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
	// Article embedding state is represented by Kafka consumer lag and outcome
	// counters; it is no longer stored in a database job table.
	var pendingOutbox int64
	if err := global.Db.Model(&models.OutboxEvent{}).Where("published_at IS NULL").Count(&pendingOutbox).Error; err != nil {
		log.Printf("[Metrics] count pending outbox: %v", err)
		return
	}
	metrics.SetOutboxPending(float64(pendingOutbox))
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
