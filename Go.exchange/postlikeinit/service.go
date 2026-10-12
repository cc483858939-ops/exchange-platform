// Package postlikeinit owns the durable initialization lifecycle shared by
// Post creation requests and the background task worker.
package postlikeinit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxJobsPerPass      = 100
	claimLease          = 10 * time.Second
	operationTimeout    = 4 * time.Second
	redisOperationLimit = 2 * time.Second
	maxRetryDelay       = 5 * time.Minute
)

var errClaimLost = errors.New("Post Like initialization task claim was lost")

var errUnsafePostBaseline = errors.New("new Post Like baseline is unsafe")

// RetryDelay returns bounded exponential backoff. Attempts are incremented
// when a durable task is claimed, so the first failure waits one second.
func RetryDelay(attempts int) time.Duration {
	delay := time.Second
	if attempts <= 1 {
		return delay
	}
	for i := 1; i < attempts && delay < maxRetryDelay; i++ {
		if delay > maxRetryDelay/2 {
			return maxRetryDelay
		}
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

// RunNow claims and processes one due task for a newly committed Post. If a
// worker already owns the lease, the request leaves the task to that worker.
func RunNow(ctx context.Context, db *gorm.DB, store *likes.Store, postID uint) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if postID == 0 {
		return likes.ErrPostLikeUnavailable
	}
	job, claimed, err := claimPost(ctx, db, postID, time.Now().UTC())
	if err != nil {
		metrics.RecordLikeLifecycleEvent("post_init_failure")
		return err
	}
	if !claimed {
		return nil
	}
	return processClaimed(ctx, db, store, job)
}

// RunPass claims at most MaxJobsPerPass due rows. Claims commit before Redis
// access, use SKIP LOCKED across worker instances, and expire if a worker dies.
func RunPass(ctx context.Context, db *gorm.DB, store *likes.Store, now time.Time) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	var passErr error
	for i := 0; i < MaxJobsPerPass; i++ {
		if err := ctx.Err(); err != nil {
			passErr = errors.Join(passErr, err)
			break
		}
		claimTime := now.UTC()
		if i > 0 {
			claimTime = time.Now().UTC()
		}
		job, claimed, err := claimNext(ctx, db, claimTime)
		if err != nil {
			metrics.RecordLikeLifecycleEvent("post_init_failure")
			passErr = errors.Join(passErr, err)
			break
		}
		if !claimed {
			break
		}
		if err := processClaimed(ctx, db, store, job); err != nil {
			passErr = errors.Join(passErr, err)
		}
	}
	if err := refreshBacklog(ctx, db); err != nil {
		metrics.RecordLikeLifecycleEvent("post_init_failure")
		passErr = errors.Join(passErr, err)
	}
	return passErr
}

func claimNext(ctx context.Context, db *gorm.DB, now time.Time) (models.PostLikeInitJob, bool, error) {
	return claim(ctx, db, 0, now)
}

func claimPost(ctx context.Context, db *gorm.DB, postID uint, now time.Time) (models.PostLikeInitJob, bool, error) {
	return claim(ctx, db, postID, now)
}

func claim(ctx context.Context, db *gorm.DB, postID uint, now time.Time) (models.PostLikeInitJob, bool, error) {
	now = now.UTC().Truncate(time.Microsecond)
	leaseUntil := now.Add(claimLease).Truncate(time.Microsecond)
	var job models.PostLikeInitJob
	claimed := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("retry_after <= ?", now).Order("retry_after, post_id").Limit(1)
		if postID != 0 {
			query = query.Where("post_id = ?", postID)
		}
		if err := query.Take(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		result := tx.Model(&models.PostLikeInitJob{}).
			Where("post_id = ? AND retry_after <= ?", job.PostID, now).
			Updates(map[string]interface{}{"retry_after": leaseUntil, "attempts": job.Attempts + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errClaimLost
		}
		job.RetryAfter = leaseUntil
		job.Attempts++
		claimed = true
		return nil
	})
	return job, claimed, err
}

func processClaimed(ctx context.Context, db *gorm.DB, store *likes.Store, job models.PostLikeInitJob) error {
	opCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()

	post, err := loadPost(opCtx, db, job.PostID, false)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && post.DeletedAt.Valid) {
		return acknowledge(opCtx, db, job)
	}
	if err != nil {
		return reschedule(ctx, db, job, err, false, false)
	}
	if store == nil {
		return reschedule(ctx, db, job, errors.New("Redis is not initialized"), false, false)
	}

	redisCtx, redisCancel := context.WithTimeout(opCtx, redisOperationLimit)
	_, stateErr := store.LoadFullState(redisCtx, job.PostID)
	redisCancel()
	if stateErr == nil {
		if job.State == models.PostLikeInitJobPending {
			if err := markAttempted(opCtx, db, job); err != nil {
				return reschedule(ctx, db, job, err, false, false)
			}
			job.State = models.PostLikeInitJobAttempted
		}
		if err := acknowledge(opCtx, db, job); err != nil {
			metrics.RecordLikeLifecycleEvent("post_init_failure")
			return err
		}
		metrics.RecordLikeLifecycleEvent("post_init_success")
		return nil
	}
	if errors.Is(stateErr, likes.ErrPostLikeUnavailable) || errors.Is(stateErr, likes.ErrLikeRedisType) {
		if job.State == models.PostLikeInitJobPending {
			if err := markAttempted(opCtx, db, job); err != nil {
				return reschedule(ctx, db, job, err, false, false)
			}
			job.State = models.PostLikeInitJobAttempted
		}
		return reschedule(ctx, db, job, stateErr, true, false)
	}
	if job.State == models.PostLikeInitJobAttempted || !errors.Is(stateErr, likes.ErrPostLikeNotReady) {
		return reschedule(ctx, db, job, stateErr, job.State == models.PostLikeInitJobAttempted, false)
	}

	if err := markAttempted(opCtx, db, job); err != nil {
		return reschedule(ctx, db, job, err, false, false)
	}
	job.State = models.PostLikeInitJobAttempted

	created, mutationPossible, initErr := initializeWhilePostLocked(opCtx, db, store, job)
	if initErr != nil {
		if errors.Is(initErr, likes.ErrPostLikeUnavailable) {
			return acknowledge(opCtx, db, job)
		}
		resetPending := canSafelyRetryInitialization(initErr, mutationPossible)
		unsafe := !resetPending || mutationPossible
		return reschedule(ctx, db, job, initErr, unsafe, resetPending)
	}
	if err := acknowledge(opCtx, db, job); err != nil {
		metrics.RecordLikeLifecycleEvent("post_init_failure")
		return err
	}
	_ = created // a ready state may have been safely recognized during initialization.
	metrics.RecordLikeLifecycleEvent("post_init_success")
	return nil
}

func loadPost(ctx context.Context, db *gorm.DB, postID uint, lock bool) (models.Post, error) {
	query := db.WithContext(ctx).Unscoped().Model(&models.Post{}).
		Select("posts.id, posts.like_count, posts.like_sync_version, posts.deleted_at").
		Where("posts.id = ?", postID)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var post models.Post
	err := query.Take(&post).Error
	return post, err
}

func markAttempted(ctx context.Context, db *gorm.DB, job models.PostLikeInitJob) error {
	result := db.WithContext(ctx).Model(&models.PostLikeInitJob{}).
		Where("post_id = ? AND retry_after = ?", job.PostID, job.RetryAfter).
		Update("state", models.PostLikeInitJobAttempted)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errClaimLost
	}
	return nil
}

func initializeWhilePostLocked(ctx context.Context, db *gorm.DB, store *likes.Store, job models.PostLikeInitJob) (bool, bool, error) {
	var created bool
	var mutationPossible bool
	redisStateConfirmed := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		post, err := loadPost(ctx, tx, job.PostID, true)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return likes.ErrPostLikeUnavailable
		}
		if err != nil {
			return err
		}
		if post.DeletedAt.Valid {
			return likes.ErrPostLikeUnavailable
		}
		var projectedLikes int64
		if err := tx.WithContext(ctx).Model(&models.PostReaction{}).
			Where("post_id = ? AND reaction = ?", job.PostID, models.PostReactionLike).
			Count(&projectedLikes).Error; err != nil {
			return err
		}
		if post.LikeCount != 0 || post.LikeSyncVersion != 0 || projectedLikes != 0 {
			return fmt.Errorf("%w: count=%d version=%d projected_likes=%d", errUnsafePostBaseline, post.LikeCount, post.LikeSyncVersion, projectedLikes)
		}
		redisCtx, cancel := context.WithTimeout(ctx, redisOperationLimit)
		defer cancel()
		created, mutationPossible, err = store.InitializeNewPostFromWithOutcome(redisCtx, job.PostID, func(context.Context) (likes.FullState, error) {
			return likes.FullState{Count: post.LikeCount, Version: post.LikeSyncVersion}, nil
		})
		if err == nil {
			redisStateConfirmed = true
		}
		return err
	})
	if err != nil && redisStateConfirmed {
		// Redis may already be Ready even if the enclosing PostgreSQL commit
		// failed. Keep the durable task in attempted state and validate only.
		mutationPossible = true
	}
	return created, mutationPossible, err
}

func canSafelyRetryInitialization(err error, mutationPossible bool) bool {
	if mutationPossible || err == nil {
		return false
	}
	for _, unsafe := range []error{
		likes.ErrLikeRecoveryUnsafe,
		likes.ErrLikeRecoveryFenceLost,
		likes.ErrPostLikeUnavailable,
		likes.ErrLikeRedisType,
		likes.ErrLikeInitializationOutcomeUnknown,
		errUnsafePostBaseline,
	} {
		if errors.Is(err, unsafe) {
			return false
		}
	}
	return true
}

func reschedule(ctx context.Context, db *gorm.DB, job models.PostLikeInitJob, cause error, unsafe bool, stateReset bool) error {
	updates := map[string]interface{}{
		"retry_after": time.Now().UTC().Add(RetryDelay(job.Attempts)).Truncate(time.Microsecond),
	}
	if stateReset {
		updates["state"] = models.PostLikeInitJobPending
	}
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	result := db.WithContext(updateCtx).Model(&models.PostLikeInitJob{}).
		Where("post_id = ? AND retry_after = ?", job.PostID, job.RetryAfter).Updates(updates)
	if result.Error != nil {
		metrics.RecordLikeLifecycleEvent("post_init_failure")
		return errors.Join(cause, result.Error)
	}
	if result.RowsAffected != 1 {
		return errors.Join(cause, errClaimLost)
	}
	metrics.RecordLikeLifecycleEvent("post_init_failure")
	metrics.RecordLikeLifecycleEvent("post_init_retry")
	if unsafe {
		metrics.RecordLikeLifecycleEvent("post_init_unsafe")
		log.Printf("[PostLikeInit] fail-closed post=%d state=%s attempts=%d err=%v", job.PostID, job.State, job.Attempts, cause)
	}
	return cause
}

func acknowledge(ctx context.Context, db *gorm.DB, job models.PostLikeInitJob) error {
	result := db.WithContext(ctx).Where("post_id = ? AND retry_after = ?", job.PostID, job.RetryAfter).
		Delete(&models.PostLikeInitJob{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errClaimLost
	}
	return nil
}

func refreshBacklog(ctx context.Context, db *gorm.DB) error {
	var backlog int64
	if err := db.WithContext(ctx).Model(&models.PostLikeInitJob{}).Count(&backlog).Error; err != nil {
		return err
	}
	metrics.SetPostLikeInitJobsBacklog(backlog)
	return nil
}
