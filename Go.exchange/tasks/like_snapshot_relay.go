package tasks

import (
	"context"
	"errors"
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

func runLikeSnapshotRelayBatch(ctx context.Context, store *likes.Store, publisher eventing.Publisher) error {
	if _, err := store.ReapExpired(ctx, config.LikeSnapshotBatchSize()); err != nil {
		return fmt.Errorf("reap expired claims: %w", err)
	}
	claims, err := store.ClaimDirty(ctx, config.LikeSnapshotBatchSize(), config.LikeClaimLease())
	if err != nil {
		return fmt.Errorf("claim dirty posts: %w", err)
	}
	if len(claims) == 0 {
		return nil
	}
	reads, err := store.LoadSnapshots(ctx, claims)
	if err != nil {
		return errors.Join(fmt.Errorf("load snapshots: %w", err), resolveLikeSnapshotClaims(ctx, store, claims, true))
	}
	pending := make([]likes.SnapshotClaim, 0, len(claims))
	retry := make([]likes.SnapshotClaim, 0)
	events := make([]eventing.Envelope, 0, len(claims))
	var firstErr error
	for _, read := range reads {
		if read.Err != nil {
			retry = append(retry, read.Claim)
			log.Printf("[LikeSnapshotRelay] load post=%d: %v", read.Claim.PostID, read.Err)
			if firstErr == nil {
				firstErr = fmt.Errorf("load post=%d: %w", read.Claim.PostID, read.Err)
			}
			continue
		}
		snapshot := read.Snapshot
		envelope, err := eventing.NewLikeSnapshotEnvelope(snapshot.PostID, snapshot.Count, snapshot.Version)
		if err != nil {
			retry = append(retry, read.Claim)
			if firstErr == nil {
				firstErr = fmt.Errorf("create post=%d envelope: %w", read.Claim.PostID, err)
			}
			continue
		}
		pending = append(pending, read.Claim)
		events = append(events, envelope)
	}
	firstErr = errors.Join(firstErr, resolveLikeSnapshotClaims(ctx, store, retry, true))
	if len(events) == 0 {
		return firstErr
	}
	if err := publishEnvelopeBatch(ctx, publisher, events); err != nil {
		return errors.Join(firstErr, fmt.Errorf("publish snapshot batch: %w", err), resolveLikeSnapshotClaims(ctx, store, pending, true))
	}
	return errors.Join(firstErr, resolveLikeSnapshotClaims(ctx, store, pending, false))
}

func resolveLikeSnapshotClaims(ctx context.Context, store *likes.Store, claims []likes.SnapshotClaim, requeue bool) error {
	if len(claims) == 0 {
		return nil
	}
	var results []likes.SnapshotClaimResult
	var err error
	operation := "ack"
	if requeue {
		operation = "requeue"
		results, err = store.RequeueClaims(ctx, claims)
	} else {
		results, err = store.AckClaims(ctx, claims)
	}
	if err != nil {
		return fmt.Errorf("%s claims: %w", operation, err)
	}
	var firstErr error
	for _, result := range results {
		if result.Err != nil {
			log.Printf("[LikeSnapshotRelay] %s post=%d claim=%s: %v", operation, result.Claim.PostID, result.Claim.ClaimID, result.Err)
			if firstErr == nil {
				firstErr = fmt.Errorf("%s post=%d: %w", operation, result.Claim.PostID, result.Err)
			}
		} else if !result.Applied {
			log.Printf("[LikeSnapshotRelay] stale %s ignored post=%d claim=%s", operation, result.Claim.PostID, result.Claim.ClaimID)
		}
	}
	return firstErr
}
