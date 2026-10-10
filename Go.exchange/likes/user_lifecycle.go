package likes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var userLikeRestoreInflight atomic.Int64

type UserLikeLifecycle struct {
	store *Store
	db    *gorm.DB
}

type userLikeRestoreStageError struct {
	result string
	err    error
}

func (e userLikeRestoreStageError) Error() string { return e.err.Error() }
func (e userLikeRestoreStageError) Unwrap() error { return e.err }

func withUserLikeRestoreStage(err error, result string) error {
	if err == nil {
		return nil
	}
	return userLikeRestoreStageError{result: result, err: err}
}

func NewUserLikeLifecycle(store *Store, db *gorm.DB) *UserLikeLifecycle {
	return &UserLikeLifecycle{store: store, db: db}
}

// RecoverIfCold restores only a Set whose missing state is proven by the
// service-timed expiry ledger. Existing ready sets are a no-op.
func (l *UserLikeLifecycle) RecoverIfCold(ctx context.Context, userID uint) error {
	if ctx == nil || l == nil || l.store == nil || l.db == nil || userID == 0 {
		return ErrUserLikeRecoveryUnsafe
	}
	settings, err := l.store.lifecycleSettings()
	if err != nil {
		return err
	}
	if !settings.RestoreEnabled {
		metrics.RecordUserLikeRestoreResult("disabled")
		return ErrUserLikeRecoveryDisabled
	}
	requestCtx, cancel := context.WithTimeout(ctx, settings.RestoreRequestTimeout)
	defer cancel()
	state, err := l.store.InspectUserLikeState(requestCtx, userID)
	if err != nil {
		stagedErr := withUserLikeRestoreStage(err, "failed_redis")
		return l.recordRestoreFailure(stagedErr, classifyRestoreFailure(stagedErr))
	}
	switch state.Status {
	case "ready":
		return nil
	case "busy":
		metrics.RecordUserLikeRestoreResult("lock_busy")
		return l.waitForRestore(requestCtx, userID)
	case "unexpected_missing":
		metrics.RecordUserLikeTTLEvent("unexpected_missing")
		return l.recordRestoreFailure(ErrUserLikeRecoveryUnsafe, "unsafe")
	case "cold":
		metrics.RecordUserLikeTTLEvent("cold_detected")
	default:
		return l.recordRestoreFailure(ErrUserLikeRecoveryUnsafe, "unsafe")
	}

	if !acquireUserLikeRestoreSlot(settings.RestoreConcurrency) {
		metrics.RecordUserLikeRestoreResult("lock_busy")
		return ErrUserLikeRecoveryBusy
	}
	defer releaseUserLikeRestoreSlot()

	token := uuid.NewString()
	status, expectedExpiry, err := l.store.beginUserLikeRestore(requestCtx, userID, token, settings.RestoreLockTTL)
	if err != nil {
		stagedErr := withUserLikeRestoreStage(err, "failed_redis")
		return l.recordRestoreFailure(stagedErr, classifyRestoreFailure(stagedErr))
	}
	switch status {
	case "ready":
		return nil
	case "busy":
		metrics.RecordUserLikeRestoreResult("lock_busy")
		return l.waitForRestore(requestCtx, userID)
	case "acquired":
	default:
		return l.recordRestoreFailure(ErrUserLikeRecoveryUnsafe, "unsafe")
	}

	started := time.Now()
	relationCount := 0
	result := "failed_redis"
	tempTTL := max(2*settings.RestoreLockTTL, 2*settings.RestoreRequestTimeout)
	tempKey, tempErr := l.store.createUserLikeRestoreTemp(requestCtx, userID, token, expectedExpiry, tempTTL)
	if tempErr != nil {
		stagedErr := withUserLikeRestoreStage(tempErr, "failed_redis")
		result = classifyRestoreFailure(stagedErr)
		l.store.releaseUserLikeRestore(requestCtx, userID, token, tempKey)
		metrics.RecordUserLikeRestoreResult(result)
		metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
		return sanitizeUserLikeRestoreError(stagedErr, result)
	}
	defer l.store.releaseUserLikeRestore(requestCtx, userID, token, tempKey)
	metrics.RecordUserLikeRestoreResult("started")

	if err := l.loadRelationsIntoTemp(requestCtx, userID, tempKey, UserLikesRestoreOrderTempKey(userID, token), settings, &relationCount); err != nil {
		result = classifyRestoreFailure(err)
		metrics.RecordUserLikeRestoreResult(result)
		metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
		return sanitizeUserLikeRestoreError(err, result)
	}
	if err := requestCtx.Err(); err != nil {
		result = "timeout"
		metrics.RecordUserLikeRestoreResult(result)
		metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
		return fmt.Errorf("%w: %v", ErrUserLikeRecoveryTimeout, err)
	}
	status, err = l.store.finishUserLikeRestore(requestCtx, userID, token, expectedExpiry, tempKey, relationCount, settings)
	if err != nil {
		stagedErr := withUserLikeRestoreStage(err, "failed_redis")
		result = classifyRestoreFailure(stagedErr)
		metrics.RecordUserLikeRestoreResult(result)
		metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
		return sanitizeUserLikeRestoreError(stagedErr, result)
	}
	if status != "installed" && status != "ready" {
		err = ErrUserLikeRecoveryUnsafe
		result = "unsafe"
		metrics.RecordUserLikeRestoreResult(result)
		metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
		return err
	}
	result = "succeeded"
	metrics.RecordUserLikeRestoreResult(result)
	metrics.ObserveUserLikeRestore(time.Since(started), relationCount)
	metrics.RecordUserLikeTTLEvent("restore_succeeded")
	return nil
}

func (l *UserLikeLifecycle) waitForRestore(ctx context.Context, userID uint) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			metrics.RecordUserLikeRestoreResult("timeout")
			return fmt.Errorf("%w: %v", ErrUserLikeRecoveryTimeout, ctx.Err())
		case <-ticker.C:
			state, err := l.store.InspectUserLikeState(ctx, userID)
			if err != nil {
				stagedErr := withUserLikeRestoreStage(err, "failed_redis")
				return l.recordRestoreFailure(stagedErr, classifyRestoreFailure(stagedErr))
			}
			switch state.Status {
			case "ready":
				return nil
			case "busy":
				continue
			case "cold":
				return ErrUserLikeRecoveryBusy
			case "unexpected_missing":
				metrics.RecordUserLikeTTLEvent("unexpected_missing")
				return l.recordRestoreFailure(ErrUserLikeRecoveryUnsafe, "unsafe")
			default:
				return ErrUserLikeRecoveryUnsafe
			}
		}
	}
}

func (l *UserLikeLifecycle) loadRelationsIntoTemp(
	ctx context.Context,
	userID uint,
	tempKey, tempOrderKey string,
	settings config.UserLikeLifecycleConfig,
	relationCount *int,
) error {
	if l.db == nil {
		return withUserLikeRestoreStage(errors.New("database is not initialized"), "failed_pg")
	}
	lastPostID := uint(0)
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Select("id").Where("id = ?", userID).Take(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserLikeRecoveryUnsafe
			}
			return withUserLikeRestoreStage(err, "failed_pg")
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			type postRow struct {
				PostID         uint
				StateChangedAt time.Time
			}
			var rows []postRow
			if err := tx.Table("post_reaction AS reaction").
				Select("reaction.post_id AS post_id, reaction.state_changed_at AS state_changed_at").
				Joins("JOIN posts ON posts.id = reaction.post_id AND posts.deleted_at IS NULL").
				Where("reaction.user_id = ? AND reaction.reaction = ? AND reaction.liked = ? AND reaction.post_id > ?",
					userID, models.PostReactionLike, true, lastPostID).
				Order("reaction.post_id ASC").Limit(settings.RestoreBatchSize).Scan(&rows).Error; err != nil {
				return withUserLikeRestoreStage(err, "failed_pg")
			}
			if len(rows) == 0 {
				return nil
			}
			if *relationCount+len(rows) > settings.RestoreMaxRelations {
				metrics.RecordUserLikeCapEvent("restore_over_cap")
				return ErrUserLikeRecoveryTooLarge
			}
			relations := make([]userLikeRestoreRelation, len(rows))
			for index, row := range rows {
				if row.PostID == 0 || row.PostID <= lastPostID || row.StateChangedAt.IsZero() {
					return ErrUserLikeRecoveryUnsafe
				}
				relations[index] = userLikeRestoreRelation{PostID: row.PostID, StateChangedAt: row.StateChangedAt}
				lastPostID = row.PostID
			}
			if err := l.store.addUserLikeRestoreTempMembers(ctx, tempKey, tempOrderKey, relations); err != nil {
				return withUserLikeRestoreStage(err, "failed_redis")
			}
			*relationCount += len(relations)
		}
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err == nil || errors.Is(err, ErrUserLikeRecoveryUnsafe) || errors.Is(err, ErrUserLikeRecoveryTooLarge) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var staged userLikeRestoreStageError
	if errors.As(err, &staged) {
		return err
	}
	return withUserLikeRestoreStage(err, "failed_pg")
}

func (l *UserLikeLifecycle) recordRestoreFailure(err error, result string) error {
	metrics.RecordUserLikeRestoreResult(result)
	return sanitizeUserLikeRestoreError(err, result)
}

func sanitizeUserLikeRestoreError(err error, result string) error {
	if result == "failed_pg" || result == "failed_redis" {
		log.Printf("[UserLikeRestore] result=%s error=%v", result, err)
		return ErrUserLikeRecoveryUnavailable
	}
	return err
}

func classifyRestoreFailure(err error) string {
	switch {
	case errors.Is(err, ErrUserLikeRecoveryTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	case errors.Is(err, ErrUserLikeRecoveryBusy):
		return "lock_busy"
	case errors.Is(err, ErrUserLikeRecoveryTooLarge):
		return "too_large"
	case errors.Is(err, ErrUserLikeRecoveryUnsafe), errors.Is(err, ErrUserLikeRecoveryLockLost), errors.Is(err, ErrUserLikeRecoveryIncomplete), errors.Is(err, ErrUserLikeNotReady), errors.Is(err, ErrLikeRedisType), errors.Is(err, ErrUserLikeOrderIndexMissing), errors.Is(err, ErrUserLikeOrderIndexInconsistent):
		return "unsafe"
	}
	var staged userLikeRestoreStageError
	if errors.As(err, &staged) {
		return staged.result
	}
	return "failed_pg"
}

func acquireUserLikeRestoreSlot(maximum int) bool {
	for {
		current := userLikeRestoreInflight.Load()
		if current >= int64(maximum) {
			return false
		}
		if userLikeRestoreInflight.CompareAndSwap(current, current+1) {
			metrics.AddUserLikeRestoreInflight(1)
			return true
		}
	}
}

func releaseUserLikeRestoreSlot() {
	userLikeRestoreInflight.Add(-1)
	metrics.AddUserLikeRestoreInflight(-1)
}
