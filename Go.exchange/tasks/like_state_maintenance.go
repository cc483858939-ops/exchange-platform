package tasks

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type likeStateMaintenanceBaseline struct {
	Count              int64
	Version            int64
	UserIDs            []uint
	ReactionRowCount   int
	MaxReactionVersion int64
	invalidVersion     bool
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
			nextCursor, err := runLikeStateMaintenancePass(ctx, store, global.Db, cursor, time.Now().UTC())
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
	_, err := runLikeStateMaintenancePass(ctx, likes.NewStore(global.RedisDB), global.Db, 0, time.Now().UTC())
	return err
}

func runLikeStateMaintenancePass(ctx context.Context, store *likes.Store, db *gorm.DB, registryCursor uint64, now time.Time) (uint64, error) {
	if store == nil {
		return registryCursor, errors.New("like state store is not initialized")
	}
	if db == nil {
		return registryCursor, errors.New("database is not initialized")
	}
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

		redisState, err := store.LoadFullState(ctx, postID)
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
		if !sameLikeState(redisState, baseline) {
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

		if !config.LikeStateExpiryEnabled() {
			log.Printf("[LikeStateMaintenance] like_state_expiry_would_arm post=%d version=%d", postID, baseline.Version)
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
		Select("posts.id,posts.like_count,posts.like_sync_version,posts.deleted_at").
		Where("posts.id IN ? AND posts.deleted_at IS NULL", ids).
		Find(&posts).Error; err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return result, nil
	}
	postByID := make(map[uint]models.Post, len(posts))
	activeIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		postByID[post.ID] = post
		activeIDs = append(activeIDs, post.ID)
		result[post.ID] = likeStateMaintenanceBaseline{UserIDs: make([]uint, 0)}
	}
	var reactions []models.PostReaction
	if err := db.Where("post_id IN ? AND reaction = ?", activeIDs, models.PostReactionLike).Find(&reactions).Error; err != nil {
		return nil, err
	}
	byPost := make(map[uint][]models.PostReaction, len(activeIDs))
	for _, reaction := range reactions {
		byPost[reaction.PostID] = append(byPost[reaction.PostID], reaction)
	}
	for postID, post := range postByID {
		baseline := likeStateMaintenanceBaseline{
			Count: post.LikeCount, Version: post.LikeSyncVersion,
			ReactionRowCount: len(byPost[postID]), UserIDs: make([]uint, 0),
		}
		seen := make(map[uint]struct{})
		for _, reaction := range byPost[postID] {
			if reaction.Version > baseline.MaxReactionVersion {
				baseline.MaxReactionVersion = reaction.Version
			}
			if reaction.Version <= 0 {
				baseline.invalidVersion = true
			}
			if reaction.Liked {
				if _, ok := seen[reaction.UserID]; !ok {
					seen[reaction.UserID] = struct{}{}
					baseline.UserIDs = append(baseline.UserIDs, reaction.UserID)
				}
			}
		}
		sort.Slice(baseline.UserIDs, func(i, j int) bool { return baseline.UserIDs[i] < baseline.UserIDs[j] })
		result[postID] = baseline
	}
	return result, nil
}

func validateLikeStateMaintenanceBaseline(baseline likeStateMaintenanceBaseline) error {
	if baseline.invalidVersion || baseline.Count < 0 || baseline.Version < 0 || baseline.ReactionRowCount < 0 {
		return likes.ErrLikeProjectionNotReady
	}
	if baseline.ReactionRowCount == 0 {
		if baseline.Count != 0 || baseline.Version != 0 || baseline.MaxReactionVersion != 0 || len(baseline.UserIDs) != 0 {
			return likes.ErrLikeProjectionNotReady
		}
		return nil
	}
	if baseline.Version <= 0 || baseline.MaxReactionVersion != baseline.Version || int64(len(baseline.UserIDs)) != baseline.Count {
		return likes.ErrLikeProjectionNotReady
	}
	return nil
}

func sameLikeState(redisState likes.FullState, baseline likeStateMaintenanceBaseline) bool {
	if redisState.Count != baseline.Count || redisState.Version != baseline.Version || len(redisState.UserIDs) != len(baseline.UserIDs) {
		return false
	}
	for index := range redisState.UserIDs {
		if redisState.UserIDs[index] != baseline.UserIDs[index] {
			return false
		}
	}
	return true
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
