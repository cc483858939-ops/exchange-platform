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
		runLikeSnapshotRelayLoop(ctx, config.LikeSnapshotPollInterval(), func(ctx context.Context) error {
			return runLikeSnapshotRelayRound(ctx, store, publisher)
		}, func(err error) {
			if err != nil && ctx.Err() == nil {
				PipelineFailure(PipelineLikeSnapshotRelay, "relay_failed", 0)
				log.Printf("[LikeSnapshotRelay] batch: %v", err)
			} else if ctx.Err() == nil {
				PipelineIdle(PipelineLikeSnapshotRelay, 0)
			}
		})
	}()
}

// One synchronous dispatcher: slow rounds cannot overlap or spawn a backlog of jobs.
func runLikeSnapshotRelayLoop(ctx context.Context, interval time.Duration, run func(context.Context) error, observe func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		observe(run(ctx))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runLikeSnapshotRelayRound(ctx context.Context, store *likes.Store, publisher eventing.Publisher) (roundErr error) {
	roundCtx, cancel := context.WithTimeout(ctx, config.LikeSnapshotRoundTimeout())
	defer cancel()
	batch, budget := config.LikeSnapshotBatchSize(), config.LikeSnapshotRoundSize()
	claims := make([]likes.SnapshotClaim, 0, budget)
	processed := 0
	// Release only unfinished claims, with a bounded context independent of a
	// canceled round. Token checks protect any newer owner after lease expiry.
	defer func() {
		if processed == len(claims) {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), min(time.Second, config.LikeSnapshotRoundTimeout()))
		defer cleanupCancel()
		for start := processed; start < len(claims); start += batch {
			end := min(start+batch, len(claims))
			roundErr = errors.Join(roundErr, resolveLikeSnapshotClaims(cleanupCtx, store, claims[start:end], true))
		}
	}()
	// Finish all bounded claims before the first ACK: re-dirtied Posts cannot
	// be repeatedly reclaimed within this round, even if publishing is fast.
	for attempt := 0; attempt < (budget+batch-1)/batch; attempt++ {
		if err := roundCtx.Err(); err != nil {
			return err
		}
		if _, err := store.ReapExpired(roundCtx, batch); err != nil {
			return fmt.Errorf("reap round claims: %w", err)
		}
		next, err := store.ClaimDirty(roundCtx, min(batch, budget-len(claims)), config.LikeClaimLease())
		if err != nil {
			return fmt.Errorf("claim round: %w", err)
		}
		claims = append(claims, next...)
		if len(next) == 0 || len(claims) == budget {
			break
		}
	}
	for processed < len(claims) {
		if err := roundCtx.Err(); err != nil {
			return err
		}
		end := min(processed+batch, len(claims))
		if err := processLikeSnapshotClaims(roundCtx, store, publisher, claims[processed:end]); err != nil {
			return err
		}
		processed = end
	}
	return nil
}

func processLikeSnapshotClaims(ctx context.Context, store *likes.Store, publisher eventing.Publisher, claims []likes.SnapshotClaim) error {
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
