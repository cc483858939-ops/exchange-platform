package tasks

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const outboxRelayBatchSize = 50

var newOutboxPublisher = func() (eventing.Publisher, error) {
	return eventing.NewKafkaPublisher(config.AppConfig.Kafka)
}

func startOutboxRelay(ctx context.Context, wg *sync.WaitGroup) {
	publisher, err := newOutboxPublisher()
	if err != nil {
		log.Printf("[Outbox] relay disabled: %v", err)
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		outboxRelayRunning.Store(true)
		defer outboxRelayRunning.Store(false)
		defer func() {
			if err := publisher.Close(); err != nil {
				log.Printf("[Outbox] close publisher: %v", err)
			}
		}()

		interval := time.Second
		if config.AppConfig.Outbox.PollIntervalSeconds > 0 {
			interval = time.Duration(config.AppConfig.Outbox.PollIntervalSeconds) * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			if err := publishPendingOutboxEvents(ctx, publisher); err != nil {
				log.Printf("[Outbox] publish pending events: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func publishPendingOutboxEvents(ctx context.Context, publisher eventing.Publisher) error {
	if global.Db == nil {
		return fmt.Errorf("database is not initialized")
	}

	var pending []models.OutboxEvent
	if err := global.Db.
		Where("published_at IS NULL").
		Order("occurred_at ASC").
		Limit(outboxRelayBatchSize).
		Find(&pending).Error; err != nil {
		return err
	}

	for _, event := range pending {
		envelope := eventing.EnvelopeFromOutbox(event)
		if err := publisher.Publish(ctx, envelope); err != nil {
			recordOutboxPublishFailure(event.ID, err)
			return fmt.Errorf("publish event %s: %w", event.ID, err)
		}
		if err := markOutboxPublished(event.ID); err != nil {
			return fmt.Errorf("mark event %s published: %w", event.ID, err)
		}
	}
	return nil
}

func markOutboxPublished(eventID string) error {
	now := time.Now().UTC()
	return global.Db.Model(&models.OutboxEvent{}).
		Where("id = ? AND published_at IS NULL", eventID).
		Updates(map[string]interface{}{
			"published_at":     now,
			"publish_attempts": gorm.Expr("publish_attempts + 1"),
			"last_error":       "",
		}).Error
}

func recordOutboxPublishFailure(eventID string, publishErr error) {
	if global.Db == nil {
		return
	}
	if err := global.Db.Model(&models.OutboxEvent{}).
		Where("id = ? AND published_at IS NULL", eventID).
		Updates(map[string]interface{}{
			"publish_attempts": gorm.Expr("publish_attempts + 1"),
			"last_error":       publishErr.Error(),
		}).Error; err != nil {
		log.Printf("[Outbox] record publish failure for %s: %v", eventID, err)
	}
}
