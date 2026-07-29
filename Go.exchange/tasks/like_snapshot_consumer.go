package tasks

import (
	"context"
	"encoding/json"
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

func startLikeSnapshotProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			runLikeSnapshotProjectionConsumer(ctx)
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func runLikeSnapshotProjectionConsumer(ctx context.Context) {
	reader, err := eventing.NewKafkaReader(config.AppConfig.Kafka, config.AppConfig.Kafka.LikeSnapshotTopic, config.AppConfig.Kafka.LikeSnapshotGroupID)
	if err != nil {
		log.Printf("[LikeSnapshotProjection] create reader: %v", err)
		return
	}
	defer reader.Close()
	likeSnapshotConsumers.Add(1)
	defer likeSnapshotConsumers.Add(-1)
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[LikeSnapshotProjection] fetch: %v", err)
			}
			return
		}
		event, err := eventing.DecodeEnvelope(message.Value)
		if err == nil && event.Type == eventing.EventTypeArticleLikeSnapshot {
			err = applyLikeSnapshotEvent(event)
		}
		if err != nil {
			log.Printf("[LikeSnapshotProjection] apply: %v", err)
			return
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			if ctx.Err() == nil {
				log.Printf("[LikeSnapshotProjection] commit: %v", err)
			}
			return
		}
	}
}

func applyLikeSnapshotEvent(event eventing.Envelope) error {
	var payload eventing.ArticleLikeSnapshotPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode like snapshot: %w", err)
	}
	if payload.ArticleID == 0 || payload.Version <= 0 || payload.LikeCount < 0 {
		return fmt.Errorf("invalid like snapshot payload")
	}
	return global.Db.Transaction(func(tx *gorm.DB) error {
		first, err := eventing.MarkInboxProcessed(tx, config.AppConfig.Kafka.LikeSnapshotGroupID, event.ID)
		if err != nil || !first {
			return err
		}
		return tx.Model(&models.Article{}).
			Where("id = ? AND like_sync_version < ?", payload.ArticleID, payload.Version).
			Updates(map[string]interface{}{"like_count": payload.LikeCount, "like_sync_version": payload.Version}).Error
	})
}
