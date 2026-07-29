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
	"Go.exchange/likes"
)

func startLikeStateRelay(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
		if err != nil {
			log.Printf("[LikeBehaviorRelay] create publisher: %v", err)
			return
		}
		defer publisher.Close()

		// A single timed dispatcher is intentional: it gives every pair a
		// coalescing window instead of immediately re-claiming it after ACK.
		// The API state remains synchronous in Redis; only behavior projection
		// waits for this bounded eventual-consistency window.
		likeBehaviorRelayWorkers.Add(1)
		defer likeBehaviorRelayWorkers.Add(-1)
		store := likes.NewStore(global.RedisDB)
		ticker := time.NewTicker(config.LikeBehaviorFlushInterval())
		defer ticker.Stop()
		for {
			if err := runLikeBehaviorRelayBatch(ctx, store, publisher); err != nil && ctx.Err() == nil {
				log.Printf("[LikeBehaviorRelay] batch: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runLikeBehaviorRelayBatch(ctx context.Context, store *likes.Store, publisher eventing.Publisher) error {
	batch := config.LikeBehaviorBatchSize()
	if _, err := store.ReapBehaviorExpired(ctx, batch); err != nil {
		return fmt.Errorf("reap expired claims: %w", err)
	}
	claims, err := store.ClaimBehaviorDirty(ctx, batch, config.LikeBehaviorClaimLease())
	if err != nil {
		return fmt.Errorf("claim dirty behavior: %w", err)
	}
	if len(claims) == 0 {
		return nil
	}
	deliveries, err := store.LoadBehaviorDeliveries(ctx, claims)
	if err != nil {
		_ = store.RequeueBehaviorClaims(ctx, claims)
		return fmt.Errorf("load behavior state: %w", err)
	}
	events := make([]eventing.Envelope, 0, len(deliveries))
	for _, delivery := range deliveries {
		action := "unlike"
		if delivery.Liked {
			action = "like"
		}
		event, err := eventing.NewLikeBehaviorEnvelope(
			fmt.Sprintf("like-state:%d:%d:%d", delivery.UserID, delivery.ArticleID, delivery.Version),
			delivery.UserID,
			delivery.ArticleID,
			action,
			delivery.Version,
			delivery.OccurredAt,
		)
		if err != nil {
			_ = store.RequeueBehaviorClaims(ctx, claims)
			return err
		}
		events = append(events, event)
	}
	if err := publishEnvelopeBatch(ctx, publisher, events); err != nil {
		_ = store.RequeueBehaviorClaims(ctx, claims)
		return fmt.Errorf("publish Kafka batch: %w", err)
	}
	if _, err := store.AckBehaviorDeliveries(ctx, deliveries); err != nil {
		return fmt.Errorf("ack behavior claims: %w", err)
	}
	return nil
}

func publishEnvelopeBatch(ctx context.Context, publisher eventing.Publisher, events []eventing.Envelope) error {
	if batchPublisher, ok := publisher.(eventing.BatchPublisher); ok {
		return batchPublisher.PublishBatch(ctx, events)
	}
	for _, event := range events {
		if err := publisher.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
