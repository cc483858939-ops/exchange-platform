package eventing

import (
	"errors"
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

// MarkInboxProcessed returns false when the same consumer already processed
// the event. It must be called in the same transaction as the projection write.
func MarkInboxProcessed(tx *gorm.DB, consumerName, eventID string) (bool, error) {
	if tx == nil {
		return false, errors.New("database transaction is nil")
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
