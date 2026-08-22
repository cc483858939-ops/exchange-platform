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
)

const outboxRetentionMaxWALLagBytes = 64 * 1024 * 1024

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
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := cleanupOutboxOnce(time.Now().UTC().Add(-retention), batchSize); err != nil && ctx.Err() == nil {
				log.Printf("[OutboxRetention] cleanup skipped: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func cleanupOutboxOnce(cutoff time.Time, batchSize int) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	if batchSize <= 0 {
		batchSize = 5000
	}
	var slot struct {
		Active      bool    `gorm:"column:active"`
		Confirmed   *string `gorm:"column:confirmed_flush_lsn"`
		WALLagBytes *int64  `gorm:"column:wal_lag_bytes"`
	}
	if err := global.Db.Raw(`
SELECT active, confirmed_flush_lsn::text,
       CASE WHEN confirmed_flush_lsn IS NULL THEN NULL ELSE pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)::bigint END AS wal_lag_bytes
FROM pg_replication_slots
WHERE slot_name = 'goexchange_outbox_slot'
`).Scan(&slot).Error; err != nil {
		return fmt.Errorf("read CDC slot health: %w", err)
	}
	if slot.Confirmed == nil || !slot.Active {
		return errors.New("CDC slot is missing, inactive, or has no confirmed flush LSN")
	}
	if slot.WALLagBytes != nil && *slot.WALLagBytes > outboxRetentionMaxWALLagBytes {
		return fmt.Errorf("CDC WAL lag %d exceeds %d bytes", *slot.WALLagBytes, outboxRetentionMaxWALLagBytes)
	}
	if cutoff.IsZero() {
		return errors.New("outbox retention cutoff is required")
	}
	result := global.Db.Exec(`
DELETE FROM outbox_events
WHERE id IN (
  SELECT id FROM outbox_events
  WHERE created_at < ?
  ORDER BY created_at ASC, id ASC
  LIMIT ?
)`, cutoff.UTC(), batchSize)
	if result.Error != nil {
		return fmt.Errorf("delete retained outbox rows: %w", result.Error)
	}
	return nil
}
