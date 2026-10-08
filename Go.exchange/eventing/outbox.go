package eventing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func AddOutboxEvent(tx *gorm.DB, event models.OutboxEvent) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	return tx.Create(&event).Error
}

const (
	outboxBatchMaxRows         = 500
	outboxBatchMaxMessageBytes = 1 << 20
)

// AddOutboxEvents writes already materialized canonical events using the
// caller's transaction. Return any error to that transaction so earlier chunks
// and projection writes roll back together. It neither commits nor deduplicates
// events. A single oversized message is written alone, preserving the existing
// single-event contract; ordinary chunks bound both parameters and JSON bytes.
func AddOutboxEvents(tx *gorm.DB, events []models.OutboxEvent) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	for start := 0; start < len(events); {
		end, messageBytes := start, 0
		for end < len(events) && end-start < outboxBatchMaxRows {
			nextBytes := len(events[end].Message)
			if end > start && messageBytes+nextBytes > outboxBatchMaxMessageBytes {
				break
			}
			messageBytes += nextBytes
			end++
		}
		batch := events[start:end]
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		start = end
	}
	return nil
}

// MarkInboxProcessed returns false when the same consumer already processed
// the event. It must be called in the same transaction as the projection write.
func MarkInboxProcessed(tx *gorm.DB, consumerName, eventID string) (bool, error) {
	if tx == nil {
		return false, errors.New("database transaction is nil")
	}
	eventID, err := NormalizeEventID(eventID)
	if err != nil {
		return false, err
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.ConsumerInbox{
		ConsumerName: consumerName,
		EventID:      eventID,
		ProcessedAt:  time.Now().UTC(),
	})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// MarkInboxProcessedBatch inserts a deduplicated set of inbox keys in one
// PostgreSQL INSERT ... ON CONFLICT ... RETURNING statement. The returned map
// contains only normalized IDs delivered for the first time to this consumer.
// Callers must use the same normalized IDs when filtering projection records.
func MarkInboxProcessedBatch(tx *gorm.DB, consumerName string, eventIDs []string) (map[string]struct{}, error) {
	if tx == nil {
		return nil, errors.New("database transaction is nil")
	}
	consumerName = strings.TrimSpace(consumerName)
	if consumerName == "" {
		return nil, errors.New("consumer name is required")
	}
	uniqueIDs := make([]string, 0, len(eventIDs))
	seen := make(map[string]struct{}, len(eventIDs))
	for _, eventID := range eventIDs {
		eventID, err := NormalizeEventID(eventID)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[eventID]; exists {
			continue
		}
		seen[eventID] = struct{}{}
		uniqueIDs = append(uniqueIDs, eventID)
	}
	firstDelivery := make(map[string]struct{}, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return firstDelivery, nil
	}

	values := make([]string, 0, len(uniqueIDs))
	args := make([]interface{}, 0, len(uniqueIDs)*3)
	processedAt := time.Now().UTC()
	for index, eventID := range uniqueIDs {
		base := index*3 + 1
		values = append(values, fmt.Sprintf("($%d, $%d, $%d)", base, base+1, base+2))
		args = append(args, consumerName, eventID, processedAt)
	}
	query := "INSERT INTO consumer_inboxes (consumer_name, event_id, processed_at) VALUES " +
		strings.Join(values, ", ") +
		" ON CONFLICT (consumer_name, event_id) DO NOTHING RETURNING event_id"
	var inserted []struct {
		EventID string `gorm:"column:event_id"`
	}
	if err := tx.Raw(query, args...).Scan(&inserted).Error; err != nil {
		return nil, err
	}
	for _, row := range inserted {
		if row.EventID != "" {
			firstDelivery[row.EventID] = struct{}{}
		}
	}
	return firstDelivery, nil
}
