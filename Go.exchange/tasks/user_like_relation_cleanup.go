package tasks

import (
	"context"
	"errors"
	"log"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	userLikeCleanupUserPageSize = 64
	userLikeCleanupScanCount    = 128
	userLikeCleanupBatchSize    = 128
)

type userLikeRelationCleanupState struct {
	lastUserID      uint
	users           []uint
	userIndex       int
	userID          uint
	scanCursor      uint64
	pending         []uint
	scanUserLikes   func(context.Context, *likes.Store, uint, uint64, int) ([]uint, uint64, error)
	removeRelations func(context.Context, *likes.Store, uint, []uint) (int64, []likes.UserLikeCleanupIssue, error)
}

func startUserLikeRelationCleanup(ctx context.Context, wg interface {
	Add(int)
	Done()
}) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		store := likes.NewStore(global.RedisDB)
		db := global.WorkerDb
		state := userLikeRelationCleanupState{}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			passCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := runUserLikeRelationCleanupPass(passCtx, store, db, &state)
			cancel()
			if err != nil && ctx.Err() == nil {
				metrics.RecordLikeLifecycleEvent("user_relation_cleanup_error")
				metrics.RecordLikeLifecycleEvent("user_relation_cleanup_retry")
				log.Printf("[UserLikeRelationCleanup] pass failed; progress retained for retry: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// runUserLikeRelationCleanupPass advances one bounded SSCAN page. Progress is
// retained in memory during normal operation; a process restart starts at the
// first SQL user again, which is safe because removals are idempotent.
func runUserLikeRelationCleanupPass(ctx context.Context, store *likes.Store, db *gorm.DB, state *userLikeRelationCleanupState) error {
	if ctx == nil {
		return errors.New("user relation cleanup context is nil")
	}
	if store == nil {
		return errors.New("user relation cleanup Redis store is not initialized")
	}
	if db == nil {
		return errors.New("user relation cleanup database is not initialized")
	}
	if state == nil {
		return errors.New("user relation cleanup progress is nil")
	}
	if state.userID == 0 {
		if err := loadNextUserPage(ctx, db, state); err != nil {
			return err
		}
		if len(state.users) == 0 {
			return nil
		}
		state.userID = state.users[state.userIndex]
	}

	if len(state.pending) == 0 {
		scan := state.scanUserLikes
		if scan == nil {
			scan = func(ctx context.Context, store *likes.Store, userID uint, cursor uint64, count int) ([]uint, uint64, error) {
				return store.ScanUserLikes(ctx, userID, cursor, count)
			}
		}
		postIDs, next, err := scan(ctx, store, state.userID, state.scanCursor, userLikeCleanupScanCount)
		if err != nil {
			if skipUserLikeCleanupUser(err) {
				metrics.RecordLikeLifecycleEvent("user_relation_cleanup_user_state_error")
				log.Printf("[UserLikeRelationCleanup] user=%d skipped for permanent Like Set state error: %v", state.userID, err)
				advanceUser(state)
				return nil
			}
			return err
		}
		state.scanCursor = next
		state.pending = postIDs
		if len(postIDs) == 0 {
			if next == 0 {
				advanceUser(state)
			}
			return nil
		}
	}

	limit := len(state.pending)
	if limit > userLikeCleanupBatchSize {
		limit = userLikeCleanupBatchSize
	}
	candidates := state.pending[:limit]
	deletedIDs, err := findDeletedPostIDs(ctx, db, candidates)
	if err != nil {
		return err
	}
	remove := state.removeRelations
	if remove == nil {
		remove = func(ctx context.Context, store *likes.Store, userID uint, postIDs []uint) (int64, []likes.UserLikeCleanupIssue, error) {
			return store.RemoveDeletedUserPostRelationsDetailed(ctx, userID, postIDs)
		}
	}
	removed, issues, err := remove(ctx, store, state.userID, deletedIDs)
	if err != nil {
		if skipUserLikeCleanupUser(err) {
			metrics.RecordLikeLifecycleEvent("user_relation_cleanup_user_state_error")
			log.Printf("[UserLikeRelationCleanup] user=%d skipped for permanent Like Set state error: %v", state.userID, err)
			advanceUser(state)
			return nil
		}
		return err
	}
	state.pending = state.pending[limit:]
	for _, issue := range issues {
		switch issue.Kind {
		case likes.UserLikeCleanupPostReadyTypeError:
			metrics.RecordLikeLifecycleEvent("user_relation_cleanup_post_state_error")
		case likes.UserLikeCleanupLifecycleMismatch, likes.UserLikeCleanupUnexpectedReady:
			metrics.RecordLikeLifecycleEvent("user_relation_cleanup_lifecycle_mismatch")
		}
		log.Printf("[UserLikeRelationCleanup] user=%d post=%d relation protected; state_issue=%s", state.userID, issue.PostID, issue.Kind)
	}
	if removed > 0 {
		metrics.RecordUserLikeRelationsRemoved(removed)
		log.Printf("[UserLikeRelationCleanup] user=%d removed=%d", state.userID, removed)
	}
	if len(state.pending) == 0 && state.scanCursor == 0 {
		advanceUser(state)
	}
	return nil
}

func skipUserLikeCleanupUser(err error) bool {
	return errors.Is(err, likes.ErrUserLikeNotReady) || errors.Is(err, likes.ErrUserLikeRedisType)
}

func loadNextUserPage(ctx context.Context, db *gorm.DB, state *userLikeRelationCleanupState) error {
	type userIDRow struct{ ID uint }
	var rows []userIDRow
	if err := db.WithContext(ctx).Unscoped().Model(&models.User{}).
		Select("id").Where("id > ?", state.lastUserID).
		Order("id ASC").Limit(userLikeCleanupUserPageSize).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		state.lastUserID = 0
		state.users = nil
		state.userIndex = 0
		state.userID = 0
		state.scanCursor = 0
		state.pending = nil
		return nil
	}
	state.users = state.users[:0]
	state.userIndex = 0
	for _, row := range rows {
		if row.ID != 0 {
			state.users = append(state.users, row.ID)
		}
	}
	if len(state.users) > 0 {
		state.lastUserID = state.users[len(state.users)-1]
	}
	return nil
}

func findDeletedPostIDs(ctx context.Context, db *gorm.DB, candidates []uint) ([]uint, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	type postLifecycleRow struct {
		ID        uint
		DeletedAt gorm.DeletedAt
	}
	var rows []postLifecycleRow
	if err := db.WithContext(ctx).Unscoped().Model(&models.Post{}).
		Select("posts.id, posts.deleted_at").Where("posts.id IN ?", candidates).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]gorm.DeletedAt, len(rows))
	for _, row := range rows {
		byID[row.ID] = row.DeletedAt
	}
	deleted := make([]uint, 0, len(candidates))
	seen := make(map[uint]struct{}, len(candidates))
	for _, postID := range candidates {
		if postID == 0 {
			continue
		}
		if _, exists := seen[postID]; exists {
			continue
		}
		seen[postID] = struct{}{}
		deletedAt, exists := byID[postID]
		if !exists || deletedAt.Valid {
			deleted = append(deleted, postID)
		}
	}
	return deleted, nil
}

func advanceUser(state *userLikeRelationCleanupState) {
	state.scanCursor = 0
	state.pending = nil
	state.userIndex++
	if state.userIndex >= len(state.users) {
		state.users = nil
		state.userIndex = 0
		state.userID = 0
		return
	}
	state.userID = state.users[state.userIndex]
}
