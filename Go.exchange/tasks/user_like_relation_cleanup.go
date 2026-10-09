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
	lastUserID uint
	users      []uint
	userIndex  int
	userID     uint
	scanCursor uint64
	pending    []uint
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
		postIDs, next, err := store.ScanUserLikes(ctx, state.userID, state.scanCursor, userLikeCleanupScanCount)
		if err != nil {
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
	removed, err := store.RemoveDeletedUserPostRelations(ctx, state.userID, deletedIDs)
	if err != nil {
		if errors.Is(err, likes.ErrUserLikeNotReady) {
			metrics.RecordLikeLifecycleEvent("user_relation_cleanup_error")
			log.Printf("[UserLikeRelationCleanup] user=%d relations lack initialization sentinel; no members removed", state.userID)
			advanceUser(state)
			return nil
		}
		return err
	}
	state.pending = state.pending[limit:]
	if removed > 0 {
		metrics.RecordUserLikeRelationsRemoved(removed)
		log.Printf("[UserLikeRelationCleanup] user=%d removed=%d", state.userID, removed)
	}
	if len(state.pending) == 0 && state.scanCursor == 0 {
		advanceUser(state)
	}
	return nil
}

func loadNextUserPage(ctx context.Context, db *gorm.DB, state *userLikeRelationCleanupState) error {
	type userIDRow struct{ ID uint }
	var rows []userIDRow
	if err := db.WithContext(ctx).Unscoped().Model(&models.User{}).
		Select("id").Where("id > ?", state.lastUserID).
		Order("id ASC").Limit(userLikeCleanupUserPageSize).Scan(&rows).Error; err != nil {
		return err
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
	state.userID = 0
	state.scanCursor = 0
	state.pending = nil
	state.userIndex++
	if state.userIndex >= len(state.users) {
		state.users = nil
		state.userIndex = 0
	}
}
