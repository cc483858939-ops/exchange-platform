package tasks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"gorm.io/gorm"
)

type recommendationTraceCleanupBatchResult struct {
	ResultRows  int64
	RequestRows int64
}

type recommendationTraceCleanupRunResult struct {
	ResultRows    int64
	RequestRows   int64
	Cycles        int
	CaughtUp      bool
	BudgetReached bool
	LockSkipped   bool
}

type recommendationTraceCleanupDeleteBatch func(context.Context, time.Time, int) (int64, error)

// RECTRCLN is reserved for singleton ownership of recommendation trace cleanup.
// The negative key sits below the recommendation profile namespace plus any
// valid non-negative user ID, and differs from migration and DevData lock keys.
const recommendationTraceCleanupAdvisoryLockKey int64 = -0x5245435452434C4E

const recommendationTraceCleanupUnlockTimeout = 5 * time.Second

type recommendationTraceCleanupPinnedConnection func(context.Context, func(*gorm.DB) error) error
type recommendationTraceCleanupAdvisoryLockOperation func(context.Context, *gorm.DB) (bool, error)

func startRecommendationTraceCleanup(ctx context.Context, wg *sync.WaitGroup) {
	cfg := recommendationTraceCleanupConfig()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			result, err := cleanupRecommendationTraceRun(ctx, cfg)
			if err != nil {
				log.Printf("[RecommendationTraceCleanup] %v", err)
			}

			timer := time.NewTimer(recommendationTraceCleanupNextDelay(cfg, result, err))
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
		}
	}()
}

func recommendationTraceCleanupNextDelay(cfg config.RecommendationTraceConfig, result recommendationTraceCleanupRunResult, runErr error) time.Duration {
	cfg = cfg.Normalized()
	if result.LockSkipped {
		return time.Duration(cfg.CleanupIntervalSeconds) * time.Second
	}
	if runErr == nil && !result.CaughtUp {
		return time.Duration(cfg.CleanupCatchupIntervalSeconds) * time.Second
	}
	return time.Duration(cfg.CleanupIntervalSeconds) * time.Second
}

func recommendationTraceCleanupConfig() config.RecommendationTraceConfig {
	if config.AppConfig == nil {
		return (config.RecommendationTraceConfig{}).Normalized()
	}
	return config.AppConfig.Recommendation.Trace.Normalized()
}

func cleanupRecommendationTraceRun(ctx context.Context, cfg config.RecommendationTraceConfig) (recommendationTraceCleanupRunResult, error) {
	workerDB := global.WorkerDb
	if workerDB == nil {
		return recommendationTraceCleanupRunResult{CaughtUp: true}, nil
	}

	startedAt := time.Now()
	result, runErr := withRecommendationTraceCleanupLock(ctx, workerDB, func(connDB *gorm.DB) (recommendationTraceCleanupRunResult, error) {
		return runRecommendationTraceCleanup(
			ctx,
			cfg,
			time.Now,
			func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteExpiredRecommendationResultTraceBatch(ctx, connDB, cutoff, limit)
			},
			func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteOldRecommendationRequestBatch(ctx, connDB, cutoff, limit)
			},
		)
	})
	recordRecommendationTraceCleanupMetrics(result, runErr, time.Since(startedAt))
	return result, runErr
}

func recordRecommendationTraceCleanupMetrics(result recommendationTraceCleanupRunResult, runErr error, duration time.Duration) {
	if result.LockSkipped {
		metrics.RecordRecommendationTraceCleanupRun("lock_skipped", duration)
		return
	}
	metrics.AddRecommendationTraceCleanupRows("result", result.ResultRows)
	metrics.AddRecommendationTraceCleanupRows("request", result.RequestRows)
	metrics.SetRecommendationTraceCleanupBacklogLikely(!result.CaughtUp)

	outcome := "caught_up"
	if runErr != nil {
		outcome = "error"
		metrics.RecordRecommendationTraceCleanupFailure()
	} else if result.BudgetReached {
		outcome = "budget_reached"
	}
	metrics.RecordRecommendationTraceCleanupRun(outcome, duration)
}

func withRecommendationTraceCleanupLock(
	ctx context.Context,
	db *gorm.DB,
	run func(*gorm.DB) (recommendationTraceCleanupRunResult, error),
) (recommendationTraceCleanupRunResult, error) {
	if db == nil {
		return recommendationTraceCleanupRunResult{}, errors.New("recommendation trace cleanup database is nil")
	}
	return withRecommendationTraceCleanupLockUsing(
		ctx,
		func(ctx context.Context, callback func(*gorm.DB) error) error {
			return db.WithContext(ctx).Connection(callback)
		},
		run,
		tryRecommendationTraceCleanupAdvisoryLock,
		unlockRecommendationTraceCleanupAdvisoryLock,
	)
}

// withRecommendationTraceCleanupLockUsing keeps lock ownership and cleanup SQL
// on the same pinned connection. The injected operations keep lifecycle edges
// unit-testable; production pins with GORM's Connection method above.
func withRecommendationTraceCleanupLockUsing(
	ctx context.Context,
	pin recommendationTraceCleanupPinnedConnection,
	run func(*gorm.DB) (recommendationTraceCleanupRunResult, error),
	tryLock recommendationTraceCleanupAdvisoryLockOperation,
	unlock recommendationTraceCleanupAdvisoryLockOperation,
) (recommendationTraceCleanupRunResult, error) {
	if ctx == nil {
		return recommendationTraceCleanupRunResult{}, errors.New("recommendation trace cleanup context is nil")
	}
	if pin == nil || run == nil || tryLock == nil || unlock == nil {
		return recommendationTraceCleanupRunResult{}, errors.New("recommendation trace cleanup lock dependency is nil")
	}

	var result recommendationTraceCleanupRunResult
	err := pin(ctx, func(connDB *gorm.DB) error {
		acquired, err := tryLock(ctx, connDB)
		if err != nil {
			return fmt.Errorf("acquire recommendation trace cleanup advisory lock: %w", err)
		}
		if !acquired {
			result = recommendationTraceCleanupRunResult{LockSkipped: true}
			return nil
		}

		var runErr error
		result, runErr = run(connDB)
		unlockCtx, cancel := context.WithTimeout(context.Background(), recommendationTraceCleanupUnlockTimeout)
		unlocked, unlockErr := unlock(unlockCtx, connDB)
		cancel()
		if unlockErr != nil {
			unlockErr = fmt.Errorf("release recommendation trace cleanup advisory lock: %w", unlockErr)
		} else if !unlocked {
			unlockErr = errors.New("release recommendation trace cleanup advisory lock: session did not hold the lock")
		}

		switch {
		case runErr != nil && unlockErr != nil:
			return errors.Join(runErr, unlockErr)
		case runErr != nil:
			return runErr
		default:
			return unlockErr
		}
	})
	return result, err
}

func tryRecommendationTraceCleanupAdvisoryLock(ctx context.Context, db *gorm.DB) (bool, error) {
	var acquired bool
	err := db.WithContext(ctx).
		Raw("SELECT pg_try_advisory_lock(?)", recommendationTraceCleanupAdvisoryLockKey).
		Scan(&acquired).Error
	return acquired, err
}

func unlockRecommendationTraceCleanupAdvisoryLock(ctx context.Context, db *gorm.DB) (bool, error) {
	var unlocked bool
	err := db.WithContext(ctx).
		Raw("SELECT pg_advisory_unlock(?)", recommendationTraceCleanupAdvisoryLockKey).
		Scan(&unlocked).Error
	return unlocked, err
}

func runRecommendationTraceCleanup(
	ctx context.Context,
	cfg config.RecommendationTraceConfig,
	now func() time.Time,
	deleteResultBatch recommendationTraceCleanupDeleteBatch,
	deleteRequestBatch recommendationTraceCleanupDeleteBatch,
) (recommendationTraceCleanupRunResult, error) {
	if deleteResultBatch == nil || deleteRequestBatch == nil {
		return recommendationTraceCleanupRunResult{}, errors.New("recommendation trace cleanup batch deleter is nil")
	}
	if now == nil {
		now = time.Now
	}
	cfg = cfg.Normalized()
	startedAt := now()
	cleanupAt := startedAt.UTC()
	requestCutoff := cleanupAt.AddDate(0, 0, -cfg.RequestRetentionDays)
	resultBatchSize := cfg.CleanupResultBatchSize
	requestBatchSize := cfg.CleanupRequestBatchSize
	timeBudget := time.Duration(cfg.CleanupRunBudgetSeconds) * time.Second

	var run recommendationTraceCleanupRunResult
	for {
		if err := ctx.Err(); err != nil {
			return run, err
		}

		run.Cycles++
		resultRows, err := deleteResultBatch(ctx, cleanupAt, resultBatchSize)
		if err != nil {
			return run, fmt.Errorf("delete expired recommendation result traces: %w", err)
		}
		cycle := recommendationTraceCleanupBatchResult{ResultRows: resultRows}
		run.ResultRows += cycle.ResultRows
		if err := ctx.Err(); err != nil {
			return run, err
		}

		requestRows, err := deleteRequestBatch(ctx, requestCutoff, requestBatchSize)
		if err != nil {
			return run, fmt.Errorf("delete expired recommendation requests: %w", err)
		}
		cycle.RequestRows = requestRows
		run.RequestRows += cycle.RequestRows
		if err := ctx.Err(); err != nil {
			return run, err
		}

		if cycle.ResultRows < int64(resultBatchSize) && cycle.RequestRows < int64(requestBatchSize) {
			run.CaughtUp = true
			return run, nil
		}

		if run.ResultRows >= int64(cfg.CleanupMaxResultRowsPerRun) ||
			run.RequestRows >= int64(cfg.CleanupMaxRequestRowsPerRun) ||
			now().Sub(startedAt) >= timeBudget {
			run.BudgetReached = true
			return run, nil
		}
	}
}

func deleteExpiredRecommendationResultTraceBatch(ctx context.Context, db *gorm.DB, cutoff time.Time, limit int) (int64, error) {
	const query = `WITH doomed AS (
		SELECT request_id, position
		FROM recommendation_result_traces
		WHERE expires_at <= ?
		ORDER BY expires_at ASC, request_id ASC, position ASC
		LIMIT ?
	)
	DELETE FROM recommendation_result_traces AS trace
	USING doomed
	WHERE trace.request_id = doomed.request_id
	  AND trace.position = doomed.position`
	result := db.WithContext(ctx).Exec(query, cutoff, limit)
	return result.RowsAffected, result.Error
}

func deleteOldRecommendationRequestBatch(ctx context.Context, db *gorm.DB, cutoff time.Time, limit int) (int64, error) {
	const query = `WITH doomed AS (
		SELECT request_id
		FROM recommendation_requests
		WHERE created_at < ?
		ORDER BY created_at ASC, request_id ASC
		LIMIT ?
	)
	DELETE FROM recommendation_requests AS request
	USING doomed
	WHERE request.request_id = doomed.request_id`
	result := db.WithContext(ctx).Exec(query, cutoff, limit)
	return result.RowsAffected, result.Error
}
