package tasks

import (
	"context"
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
		publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
		if err != nil {
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
			runLikeSnapshotRelayBatch(ctx, store, publisher)
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

func runLikeSnapshotRelayBatch(ctx context.Context, store *likes.Store, publisher eventing.Publisher) {
	if _, err := store.ReapExpired(ctx, config.LikeSnapshotBatchSize()); err != nil {
		log.Printf("[LikeSnapshotRelay] reap expired claims: %v", err)
		return
	}
	claims, err := store.ClaimDirty(ctx, config.LikeSnapshotBatchSize(), config.LikeClaimLease())
	if err != nil {
		log.Printf("[LikeSnapshotRelay] claim dirty articles: %v", err)
		return
	}
	pending := make([]pendingLikeSnapshot, 0, len(claims))
	events := make([]eventing.Envelope, 0, len(claims))
	for _, claim := range claims {
		snapshot, err := store.LoadSnapshot(ctx, claim.ArticleID)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] load article=%d claim=%s: %v", claim.ArticleID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue article=%d: %v", claim.ArticleID, requeueErr)
			}
			continue
		}
		envelope, err := eventing.NewLikeSnapshotEnvelope(snapshot.ArticleID, snapshot.Count, snapshot.Version)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] envelope article=%d claim=%s: %v", claim.ArticleID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue article=%d: %v", claim.ArticleID, requeueErr)
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
				log.Printf("[LikeSnapshotRelay] requeue article=%d: %v", item.claim.ArticleID, requeueErr)
			}
		}
		return
	}
	for _, item := range pending {
		acked, err := store.AckClaim(ctx, item.claim)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] ack article=%d claim=%s: %v", item.claim.ArticleID, item.claim.ClaimID, err)
			continue
		}
		if !acked {
			log.Printf("[LikeSnapshotRelay] stale claim ignored article=%d claim=%s", item.claim.ArticleID, item.claim.ClaimID)
		}
	}
}
