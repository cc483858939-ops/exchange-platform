package postmediagc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"Go.exchange/postmediaupload"
	"gorm.io/gorm"
)

var ErrRowsRetryScheduled = errors.New("one or more Post media cleanup rows failed")

type Store interface {
	ClaimCleanupBatch(context.Context, time.Time, time.Duration, int) ([]postmediaupload.CleanupClaim, error)
	CompleteCleanup(context.Context, postmediaupload.CleanupClaim) error
	ScheduleCleanupRetry(context.Context, postmediaupload.CleanupClaim, time.Time, string) error
}

type ObjectDeleter interface {
	DeleteObject(context.Context, string) error
}

type GormStore struct {
	DB *gorm.DB
}

func NewGormStore(db *gorm.DB) *GormStore { return &GormStore{DB: db} }

func (store *GormStore) ClaimCleanupBatch(ctx context.Context, now time.Time, timeout time.Duration, batchSize int) ([]postmediaupload.CleanupClaim, error) {
	return postmediaupload.ClaimCleanupBatch(ctx, store.DB, now, timeout, batchSize)
}

func (store *GormStore) CompleteCleanup(ctx context.Context, claim postmediaupload.CleanupClaim) error {
	return postmediaupload.CompleteCleanup(ctx, store.DB, claim)
}

func (store *GormStore) ScheduleCleanupRetry(ctx context.Context, claim postmediaupload.CleanupClaim, retryAt time.Time, reason string) error {
	return postmediaupload.ScheduleCleanupRetry(ctx, store.DB, claim, retryAt, reason)
}

type Summary struct {
	Claimed        int
	Cleaned        int
	RetryScheduled int
	StaleClaims    int
	Duration       time.Duration
}

type Runner struct {
	store   Store
	deleter ObjectDeleter
	cfg     Config
	now     func() time.Time
	logf    func(string, ...interface{})
}

func NewRunner(store Store, deleter ObjectDeleter, cfg Config) *Runner {
	return &Runner{store: store, deleter: deleter, cfg: cfg, now: time.Now, logf: log.Printf}
}

func (runner *Runner) Run(ctx context.Context) (Summary, error) {
	if runner == nil || runner.store == nil || runner.deleter == nil {
		return Summary{}, errors.New("Post media GC dependencies are not initialized")
	}
	if ctx == nil {
		return Summary{}, errors.New("Post media GC context is nil")
	}
	if err := runner.cfg.Validate(); err != nil {
		return Summary{}, err
	}

	started := time.Now()
	var summary Summary
	var infraErrors []error
	rowFailure := false
	for summary.Claimed < runner.cfg.MaxRowsPerRun {
		if err := ctx.Err(); err != nil {
			infraErrors = append(infraErrors, err)
			break
		}
		limit := runner.cfg.BatchSize
		if remaining := runner.cfg.MaxRowsPerRun - summary.Claimed; limit > remaining {
			limit = remaining
		}
		claims, err := runner.store.ClaimCleanupBatch(ctx, runner.now().UTC(), runner.cfg.ClaimTimeout, limit)
		if err != nil {
			infraErrors = append(infraErrors, fmt.Errorf("claim cleanup batch: %w", err))
			break
		}
		if len(claims) == 0 {
			break
		}
		summary.Claimed += len(claims)
		for _, claim := range claims {
			if err := runner.deleteClaimedObjects(ctx, claim); err != nil {
				reason := postmediaupload.CleanupErrorStorageDeleteFailure
				retryAt := runner.now().Add(RetryDelay(claim.CleanupAttempts, runner.cfg.RetryBase, runner.cfg.RetryMax))
				if retryErr := runner.store.ScheduleCleanupRetry(ctx, claim, retryAt, reason); errors.Is(retryErr, postmediaupload.ErrStaleCleanupClaim) {
					summary.StaleClaims++
					runner.logf("post-media-gc stale retry ignored media_id=%s attempt=%d", claim.MediaID, claim.CleanupAttempts)
				} else if retryErr != nil {
					infraErrors = append(infraErrors, fmt.Errorf("schedule cleanup retry for %s: %w", claim.MediaID, retryErr))
				} else {
					summary.RetryScheduled++
					rowFailure = true
					runner.logf("post-media-gc retry scheduled media_id=%s attempt=%d error_category=%s", claim.MediaID, claim.CleanupAttempts, reason)
				}
				continue
			}

			if err := runner.store.CompleteCleanup(ctx, claim); errors.Is(err, postmediaupload.ErrStaleCleanupClaim) {
				summary.StaleClaims++
				runner.logf("post-media-gc stale completion ignored media_id=%s attempt=%d", claim.MediaID, claim.CleanupAttempts)
			} else if err != nil {
				infraErrors = append(infraErrors, fmt.Errorf("complete cleanup for %s: %w", claim.MediaID, err))
			} else {
				summary.Cleaned++
			}
		}
	}
	summary.Duration = time.Since(started)
	if len(infraErrors) > 0 {
		if rowFailure {
			infraErrors = append(infraErrors, ErrRowsRetryScheduled)
		}
		return summary, errors.Join(infraErrors...)
	}
	if rowFailure {
		return summary, ErrRowsRetryScheduled
	}
	return summary, nil
}

func (runner *Runner) deleteClaimedObjects(ctx context.Context, claim postmediaupload.CleanupClaim) error {
	keys := []string{
		claim.OriginalObjectKey,
		claim.MediumObjectKey,
		claim.LargeObjectKey,
		claim.ManifestObjectKey,
	}
	var failures int
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			failures++
			continue
		}
		objectCtx, cancel := context.WithTimeout(ctx, runner.cfg.ObjectTimeout)
		err := runner.deleter.DeleteObject(objectCtx, key)
		cancel()
		if err != nil {
			failures++
		}
	}
	if failures > 0 {
		return errors.New("one or more stored objects could not be deleted")
	}
	return nil
}
