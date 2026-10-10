package tasks

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	pipelineHealthMetricsInterval   = 10 * time.Second
	pipelineRetainedMetricsInterval = 5 * time.Minute
	pipelineMetricsSampleTimeout    = 5 * time.Second
)

func startPipelineMetrics(ctx context.Context, wg *sync.WaitGroup) {
	// Historical COUNTs must not hold up the fast health sampling loop.
	startPipelineMetricsSampler(ctx, wg, pipelineHealthMetricsInterval, pipelineMetricsSampleTimeout, refreshPipelineMetrics)
	startPipelineMetricsSampler(ctx, wg, pipelineRetainedMetricsInterval, pipelineMetricsSampleTimeout, refreshPipelineRetainedMetrics)
	startPipelineMetricsSampler(ctx, wg, pipelineHealthMetricsInterval, pipelineMetricsSampleTimeout, refreshKafkaCommittedBacklogs)
}

func startPipelineMetricsSampler(ctx context.Context, wg *sync.WaitGroup, interval, timeout time.Duration, refresh func(context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			sampleCtx, cancel := context.WithTimeout(ctx, timeout)
			refresh(sampleCtx)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func refreshPipelineMetrics(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	refreshLikeQueueDepthMetrics(ctx)
	if global.WorkerDb == nil {
		return
	}
	db := global.WorkerDb.WithContext(ctx)
	var oldest struct {
		CreatedAt *time.Time `gorm:"column:created_at"`
	}
	if err := db.Model(&models.OutboxEvent{}).Select("MIN(created_at) AS created_at").Scan(&oldest).Error; err != nil {
		metrics.RecordOutboxOldestAgeSample(0, time.Now().UTC(), false)
		log.Printf("[Metrics] read oldest outbox row: %v", err)
	} else {
		age := float64(0)
		if oldest.CreatedAt != nil {
			age = time.Since(oldest.CreatedAt.UTC()).Seconds()
		}
		if age < 0 {
			age = 0
		}
		metrics.RecordOutboxOldestAgeSample(age, time.Now().UTC(), true)
	}
	refreshOutboxCDCMetrics(ctx, db)
	var dirtyProfiles int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Count(&dirtyProfiles).Error; err != nil {
		log.Printf("[Metrics] count dirty recommendation profiles: %v", err)
	} else {
		metrics.SetRecommendationProfileDirtyQueueDepth(float64(dirtyProfiles))
	}
}

func refreshLikeQueueDepthMetrics(ctx context.Context) {
	if global.RedisDB != nil {
		if dirty, err := global.RedisDB.WithContext(ctx).SCard(likes.DirtyKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("dirty", float64(dirty))
		}
		if processing, err := global.RedisDB.WithContext(ctx).ZCard(likes.ProcessingKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("processing", float64(processing))
		}
		if dirty, err := global.RedisDB.WithContext(ctx).SCard(likes.BehaviorDirtyKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_dirty", float64(dirty))
		}
		if processing, err := global.RedisDB.WithContext(ctx).ZCard(likes.BehaviorProcessingKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_processing", float64(processing))
		}
		if states, err := global.RedisDB.WithContext(ctx).HLen(likes.BehaviorStateKey).Result(); err == nil {
			metrics.SetLikePipelineDepth("behavior_state", float64(states))
		}
	}
}

func refreshPipelineRetainedMetrics(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil || global.WorkerDb == nil {
		return
	}
	db := global.WorkerDb.WithContext(ctx)
	var outboxRows int64
	if err := db.Model(&models.OutboxEvent{}).Count(&outboxRows).Error; err != nil {
		log.Printf("[Metrics] count retained outbox rows: %v", err)
	} else {
		metrics.SetOutboxRowsTotal(float64(outboxRows))
		metrics.SetOutboxRowsSampleSuccess(time.Now().UTC())
	}
	refreshNotificationInboxMetrics(ctx, db)
}

func refreshNotificationInboxMetrics(ctx context.Context, db *gorm.DB) {
	if ctx == nil || ctx.Err() != nil || db == nil {
		return
	}
	consumerName := "goexchange-notification-projection-v1"
	if config.AppConfig != nil && config.AppConfig.Kafka.NotificationGroupID != "" {
		consumerName = config.AppConfig.Kafka.NotificationGroupID
	}
	var inboxRows int64
	if err := db.WithContext(ctx).Table("consumer_inboxes").Where("consumer_name = ?", consumerName).Count(&inboxRows).Error; err != nil {
		log.Printf("[Metrics] count notification ConsumerInbox rows: %v", err)
	} else {
		metrics.SetConsumerInboxRows(consumerName, float64(inboxRows))
		metrics.SetConsumerInboxRowsSampleSuccess(consumerName, time.Now().UTC())
	}
}

func refreshOutboxCDCMetrics(ctx context.Context, db *gorm.DB) {
	if ctx == nil || ctx.Err() != nil || db == nil {
		return
	}
	var row struct {
		Active       bool            `gorm:"column:active"`
		ConfirmedLSN sql.NullFloat64 `gorm:"column:confirmed_lsn"`
		WALLagBytes  sql.NullFloat64 `gorm:"column:wal_lag_bytes"`
	}
	err := db.WithContext(ctx).Raw(`
SELECT active,
       CASE WHEN confirmed_flush_lsn IS NULL THEN NULL ELSE pg_wal_lsn_diff(confirmed_flush_lsn, '0/0') END AS confirmed_lsn,
       CASE WHEN confirmed_flush_lsn IS NULL THEN NULL ELSE pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn) END AS wal_lag_bytes
FROM pg_replication_slots
WHERE slot_name = 'goexchange_outbox_slot'
`).Scan(&row).Error
	if err != nil {
		log.Printf("[Metrics] read outbox CDC slot: %v", err)
		metrics.SetOutboxCDCSlotActive(0)
		metrics.SetOutboxCDCSlotConfirmedLSN(0)
		metrics.SetOutboxCDCWALLagBytes(0)
		return
	}
	if row.Active {
		metrics.SetOutboxCDCSlotActive(1)
	} else {
		metrics.SetOutboxCDCSlotActive(0)
	}
	if row.ConfirmedLSN.Valid {
		metrics.SetOutboxCDCSlotConfirmedLSN(row.ConfirmedLSN.Float64)
	} else {
		metrics.SetOutboxCDCSlotConfirmedLSN(0)
	}
	if row.WALLagBytes.Valid && row.WALLagBytes.Float64 >= 0 {
		metrics.SetOutboxCDCWALLagBytes(row.WALLagBytes.Float64)
	} else {
		metrics.SetOutboxCDCWALLagBytes(0)
	}
}
