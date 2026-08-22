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
	var outboxRows int64
	if err := global.Db.Model(&models.OutboxEvent{}).Count(&outboxRows).Error; err != nil {
		log.Printf("[Metrics] count retained outbox rows: %v", err)
	} else {
		metrics.SetOutboxRowsTotal(float64(outboxRows))
	}
	var oldest struct {
		CreatedAt *time.Time `gorm:"column:created_at"`
	}
	if err := global.Db.Model(&models.OutboxEvent{}).Select("MIN(created_at) AS created_at").Scan(&oldest).Error; err == nil && oldest.CreatedAt != nil {
		age := time.Since(oldest.CreatedAt.UTC()).Seconds()
		if age < 0 {
			age = 0
		}
		metrics.SetOutboxOldestRowAgeSeconds(age)
	} else {
		metrics.SetOutboxOldestRowAgeSeconds(0)
	}
	refreshOutboxCDCMetrics()
	refreshNotificationProjectionMetrics()
	var dirtyProfiles int64
	if err := global.Db.Model(&models.UserRecoProfileDirty{}).Count(&dirtyProfiles).Error; err != nil {
		log.Printf("[Metrics] count dirty recommendation profiles: %v", err)
	} else {
		metrics.SetRecommendationProfileDirtyQueueDepth(float64(dirtyProfiles))
	}
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

func refreshNotificationProjectionMetrics() {
	consumerName := "goexchange-notification-projection-v1"
	if config.AppConfig != nil && config.AppConfig.Kafka.NotificationGroupID != "" {
		consumerName = config.AppConfig.Kafka.NotificationGroupID
	}
	var inboxRows int64
	if err := global.Db.Table("consumer_inboxes").Where("consumer_name = ?", consumerName).Count(&inboxRows).Error; err != nil {
		log.Printf("[Metrics] count notification ConsumerInbox rows: %v", err)
	} else {
		metrics.SetConsumerInboxRows(consumerName, float64(inboxRows))
	}
}

func refreshOutboxCDCMetrics() {
	var row struct {
		Active       bool            `gorm:"column:active"`
		ConfirmedLSN sql.NullFloat64 `gorm:"column:confirmed_lsn"`
		WALLagBytes  sql.NullFloat64 `gorm:"column:wal_lag_bytes"`
	}
	err := global.Db.Raw(`
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
