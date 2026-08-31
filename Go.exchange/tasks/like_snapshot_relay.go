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

func startLikeSnapshotRelay(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelineLikeSnapshotRelay)
		defer PipelineStopped(PipelineLikeSnapshotRelay)
		publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
		if err != nil {
			PipelineFailure(PipelineLikeSnapshotRelay, "kafka_publisher_unavailable", 0)
			log.Printf("[LikeSnapshotRelay] create publisher: %v", err)
			return
		}
		defer publisher.Close()
		likeSnapshotRelayRunning.Store(true)
		defer likeSnapshotRelayRunning.Store(false)
		store := likes.NewStore(global.RedisDB)
		interval := config.LikeSnapshotPollInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := runLikeSnapshotRelayBatch(ctx, store, publisher); err != nil && ctx.Err() == nil {
				PipelineFailure(PipelineLikeSnapshotRelay, "relay_failed", 0)
				log.Printf("[LikeSnapshotRelay] batch: %v", err)
			} else if ctx.Err() == nil {
				PipelineIdle(PipelineLikeSnapshotRelay, 0)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

type pendingLikeSnapshot struct {
	claim    likes.SnapshotClaim
	envelope eventing.Envelope
}

func runLikeSnapshotRelayBatch(ctx context.Context, store *likes.Store, publisher eventing.Publisher) error {
	if _, err := store.ReapExpired(ctx, config.LikeSnapshotBatchSize()); err != nil {
		log.Printf("[LikeSnapshotRelay] reap expired claims: %v", err)
		return fmt.Errorf("reap expired claims: %w", err)
	}
	claims, err := store.ClaimDirty(ctx, config.LikeSnapshotBatchSize(), config.LikeClaimLease())
	if err != nil {
		log.Printf("[LikeSnapshotRelay] claim dirty posts: %v", err)
		return fmt.Errorf("claim dirty posts: %w", err)
	}
	pending := make([]pendingLikeSnapshot, 0, len(claims))
	events := make([]eventing.Envelope, 0, len(claims))
	var firstErr error
	for _, claim := range claims {
		snapshot, err := store.LoadSnapshot(ctx, claim.PostID)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] load post=%d claim=%s: %v", claim.PostID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", claim.PostID, requeueErr)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("load post=%d: %w", claim.PostID, err)
			}
			continue
		}
		envelope, err := eventing.NewLikeSnapshotEnvelope(snapshot.PostID, snapshot.Count, snapshot.Version)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] envelope post=%d claim=%s: %v", claim.PostID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", claim.PostID, requeueErr)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("create post=%d envelope: %w", claim.PostID, err)
			}
			continue
		}
		pending = append(pending, pendingLikeSnapshot{claim: claim, envelope: envelope})
		events = append(events, envelope)
	}
	if err := publishEnvelopeBatch(ctx, publisher, events); err != nil {
		log.Printf("[LikeSnapshotRelay] publish batch: %v", err)
		for _, item := range pending {
			if _, requeueErr := store.RequeueClaim(ctx, item.claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", item.claim.PostID, requeueErr)
			}
		}
		return fmt.Errorf("publish snapshot batch: %w", err)
	}
	for _, item := range pending {
		acked, err := store.AckClaim(ctx, item.claim)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] ack post=%d claim=%s: %v", item.claim.PostID, item.claim.ClaimID, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("ack post=%d claim=%s: %w", item.claim.PostID, item.claim.ClaimID, err)
			}
			continue
		}
		if !acked {
			log.Printf("[LikeSnapshotRelay] stale claim ignored post=%d claim=%s", item.claim.PostID, item.claim.ClaimID)
		}
	}
	return firstErr
}
