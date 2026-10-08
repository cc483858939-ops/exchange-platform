package tasks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"gorm.io/gorm"
)

const outboxRetentionMaxWALLagBytes = 64 * 1024 * 1024

const outboxRetentionCatchupDelay = 5 * time.Second

func startOutboxRetention(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := 10 * time.Minute
		batchSize := 5000
		retention := 24 * time.Hour
		if config.AppConfig != nil {
			if config.AppConfig.Outbox.CleanupIntervalSeconds > 0 {
				interval = time.Duration(config.AppConfig.Outbox.CleanupIntervalSeconds) * time.Second
			}
			if config.AppConfig.Outbox.CleanupBatchSize > 0 {
				batchSize = config.AppConfig.Outbox.CleanupBatchSize
			}
			if config.AppConfig.Outbox.RetentionHours > 0 {
				retention = time.Duration(config.AppConfig.Outbox.RetentionHours) * time.Hour
			}
		}
		for {
			started := time.Now()
			rows, err := cleanupOutboxOnce(ctx, started.UTC().Add(-retention), batchSize)
			metrics.RecordOutboxRetentionBatch(rows, batchSize, time.Since(started), err)
			if err != nil && ctx.Err() == nil {
				log.Printf("[OutboxRetention] cleanup skipped: %v", err)
			}
			timer := time.NewTimer(outboxRetentionNextDelay(interval, batchSize, rows, err))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// Full batches indicate possible backlog. Catch up in separate small batches,
// checking CDC health again each time, rather than increasing transaction size.
func outboxRetentionNextDelay(interval time.Duration, batchSize int, rows int64, err error) time.Duration {
	if err == nil && rows >= int64(batchSize) {
		return min(interval, outboxRetentionCatchupDelay)
	}
	return interval
}

func cleanupOutboxOnce(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if ctx == nil {
		return 0, errors.New("outbox retention context is nil")
	}
	if global.WorkerDb == nil {
		return 0, errors.New("database is not initialized")
	}
	if batchSize <= 0 {
		batchSize = 5000
	}
	db := global.WorkerDb.WithContext(ctx)
	var slot struct {
		Active      bool    `gorm:"column:active"`
		Confirmed   *string `gorm:"column:confirmed_flush_lsn"`
		WALLagBytes *int64  `gorm:"column:wal_lag_bytes"`
	}
	if err := db.Raw(`
SELECT active, confirmed_flush_lsn::text,
       CASE WHEN confirmed_flush_lsn IS NULL THEN NULL ELSE pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)::bigint END AS wal_lag_bytes
FROM pg_replication_slots
WHERE slot_name = 'goexchange_outbox_slot'
`).Scan(&slot).Error; err != nil {
		return 0, fmt.Errorf("read CDC slot health: %w", err)
	}
	if slot.Confirmed == nil || !slot.Active {
		return 0, errors.New("CDC slot is missing, inactive, or has no confirmed flush LSN")
	}
	if slot.WALLagBytes != nil && *slot.WALLagBytes > outboxRetentionMaxWALLagBytes {
		return 0, fmt.Errorf("CDC WAL lag %d exceeds %d bytes", *slot.WALLagBytes, outboxRetentionMaxWALLagBytes)
	}
	if cutoff.IsZero() {
		return 0, errors.New("outbox retention cutoff is required")
	}
	return deleteRetainedOutboxRows(db, cutoff, batchSize)
}

func deleteRetainedOutboxRows(db *gorm.DB, cutoff time.Time, batchSize int) (int64, error) {
	result := db.Exec(`
DELETE FROM outbox_events
WHERE id IN (
  SELECT id FROM outbox_events
  WHERE created_at < ?
  ORDER BY created_at ASC, id ASC
  LIMIT ?
)`, cutoff.UTC(), batchSize)
	if result.Error != nil {
		return 0, fmt.Errorf("delete retained outbox rows: %w", result.Error)
	}
	return result.RowsAffected, nil
}
