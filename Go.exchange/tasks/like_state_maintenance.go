package tasks

import (
	"context"
	"errors"
	"log"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type likeStateMaintenanceBaseline struct {
	Count   int64
	Version int64
}

func startLikeStateMaintenance(ctx context.Context, wg interface {
	Add(int)
	Done()
}) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelineLikeStateMaintenance)
		defer PipelineStopped(PipelineLikeStateMaintenance)

		store := likes.NewStore(global.RedisDB)
		interval := config.LikeStateMaintenanceInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var cursor uint64
		for {
			nextCursor, err := runLikeStateMaintenancePass(ctx, store, global.WorkerDb, cursor, time.Now().UTC())
			if err != nil && ctx.Err() == nil {
				PipelineFailure(PipelineLikeStateMaintenance, "maintenance_failed", 0)
				log.Printf("[LikeStateMaintenance] pass: %v", err)
			} else if ctx.Err() == nil {
				PipelineIdle(PipelineLikeStateMaintenance, 0)
			}
			cursor = nextCursor
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runLikeStateMaintenance(ctx context.Context) error {
	_, err := runLikeStateMaintenancePass(ctx, likes.NewStore(global.RedisDB), global.WorkerDb, 0, time.Now().UTC())
	return err
}

func runLikeStateMaintenancePass(ctx context.Context, store *likes.Store, db *gorm.DB, registryCursor uint64, now time.Time) (uint64, error) {
	if ctx == nil {
		return registryCursor, errors.New("like state maintenance context is nil")
	}
	if store == nil {
		return registryCursor, errors.New("like state store is not initialized")
	}
	if db == nil {
		return registryCursor, errors.New("database is not initialized")
	}
	db = db.WithContext(ctx)
	if now.IsZero() {
		now = time.Now().UTC()
	}

	nextCursor, err := reconcileLikeStateRegistry(ctx, store, db, registryCursor, config.LikeStateMaintenanceBatchSize())
	if err != nil {
		return nextCursor, err
	}
	if err := verifyIdleLikeStates(ctx, store, db, now); err != nil {
		return nextCursor, err
	}
	return nextCursor, nil
}

func reconcileLikeStateRegistry(ctx context.Context, store *likes.Store, db *gorm.DB, cursor uint64, batch int) (uint64, error) {
	postIDs, nextCursor, err := store.ScanRegistry(ctx, cursor, batch)
	if err != nil {
		return cursor, err
	}
	if len(postIDs) == 0 {
		return nextCursor, nil
	}
	activeIDs, err := loadStructurallyActivePostIDs(db, postIDs)
	if err != nil {
		return cursor, err
	}
	active := make(map[uint]struct{}, len(activeIDs))
	for _, postID := range activeIDs {
		active[postID] = struct{}{}
	}
	for _, postID := range postIDs {
		if _, ok := active[postID]; ok {
			continue
		}
		if err := store.PurgePost(ctx, postID); err != nil {
			log.Printf("[LikeStateMaintenance] like_state_lua_type_preflight_failed post=%d purge: %v", postID, err)
			return cursor, err
		}
		log.Printf("[LikeStateMaintenance] like_state_deleted_reconciled post=%d", postID)
	}
	return nextCursor, nil
}

func verifyIdleLikeStates(ctx context.Context, store *likes.Store, db *gorm.DB, now time.Time) error {
	if !config.LikeStateExpiryEnabled() {
		return nil
	}
	cutoff := now.Add(-config.LikeStateIdleBeforeExpiry())
	postIDs, err := store.LoadExpiryCandidates(ctx, cutoff, config.LikeStateMaintenanceBatchSize())
	if err != nil {
		return err
	}
	if len(postIDs) == 0 {
		return nil
	}
	baselines, err := loadLikeStateMaintenanceBaselines(db, postIDs)
	if err != nil {
		return err
	}
	for _, postID := range postIDs {
		baseline, active := baselines[postID]
		if !active {
			if err := store.PurgePost(ctx, postID); err != nil {
				return err
			}
			log.Printf("[LikeStateMaintenance] like_state_deleted_reconciled post=%d", postID)
			continue
		}
		if err := validateLikeStateMaintenanceBaseline(baseline); err != nil {
			log.Printf("[LikeStateMaintenance] like_state_expiry_mismatch post=%d reason=projection_not_ready", postID)
			if touchErr := store.TouchExpiryCandidate(ctx, postID, now); touchErr != nil {
				return touchErr
			}
			continue
		}

		redisState, err := store.LoadSummary(ctx, postID)
		if err != nil {
			if errors.Is(err, likes.ErrNotReady) {
				log.Printf("[LikeStateMaintenance] like_state_expiry_mismatch post=%d reason=redis_not_ready", postID)
				if touchErr := store.TouchExpiryCandidate(ctx, postID, now); touchErr != nil {
					return touchErr
				}
				continue
			}
			return err
		}
		if redisState.Count != baseline.Count || redisState.Version != baseline.Version {
			log.Printf("[LikeStateMaintenance] like_state_expiry_mismatch post=%d reason=state_not_equal", postID)
			if touchErr := store.TouchExpiryCandidate(ctx, postID, now); touchErr != nil {
				return touchErr
			}
			continue
		}
		quiescent, err := store.SnapshotQueueQuiescent(ctx, postID)
		if err != nil {
			return err
		}
		if !quiescent {
			log.Printf("[LikeStateMaintenance] like_state_expiry_queue_busy post=%d", postID)
			if touchErr := store.TouchExpiryCandidate(ctx, postID, now); touchErr != nil {
				return touchErr
			}
			continue
		}

		armed, err := store.ArmExpiry(ctx, postID, baseline.Version, config.LikeStateTTL())
		if err != nil {
			return err
		}
		if !armed {
			log.Printf("[LikeStateMaintenance] like_state_expiry_mismatch post=%d reason=version_or_queue_race", postID)
			if touchErr := store.TouchExpiryCandidate(ctx, postID, now); touchErr != nil {
				return touchErr
			}
			continue
		}
		log.Printf("[LikeStateMaintenance] like_state_expiry_armed post=%d version=%d", postID, baseline.Version)
	}
	return nil
}

func loadStructurallyActivePostIDs(db *gorm.DB, postIDs []uint) ([]uint, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	if len(postIDs) == 0 {
		return nil, nil
	}
	var activeIDs []uint
	if err := db.Unscoped().Model(&models.Post{}).
		Where("posts.id IN ? AND posts.deleted_at IS NULL", postIDs).
		Pluck("posts.id", &activeIDs).Error; err != nil {
		return nil, err
	}
	return activeIDs, nil
}

func loadLikeStateMaintenanceBaselines(db *gorm.DB, postIDs []uint) (map[uint]likeStateMaintenanceBaseline, error) {
	result := make(map[uint]likeStateMaintenanceBaseline, len(postIDs))
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	if len(postIDs) == 0 {
		return result, nil
	}
	ids := uniqueMaintenancePostIDs(postIDs)
	var posts []models.Post
	if err := db.Unscoped().Model(&models.Post{}).
		Select("posts.id,posts.like_count,posts.like_sync_version").
		Where("posts.id IN ? AND posts.deleted_at IS NULL", ids).
		Find(&posts).Error; err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return result, nil
	}
	for _, post := range posts {
		result[post.ID] = likeStateMaintenanceBaseline{Count: post.LikeCount, Version: post.LikeSyncVersion}
	}
	return result, nil
}

func validateLikeStateMaintenanceBaseline(baseline likeStateMaintenanceBaseline) error {
	if baseline.Count < 0 || baseline.Version < 0 {
		return likes.ErrLikeProjectionNotReady
	}
	return nil
}

func uniqueMaintenancePostIDs(postIDs []uint) []uint {
	seen := make(map[uint]struct{}, len(postIDs))
	result := make([]uint, 0, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		if _, ok := seen[postID]; ok {
			continue
		}
		seen[postID] = struct{}{}
		result = append(result, postID)
	}
	return result
}
