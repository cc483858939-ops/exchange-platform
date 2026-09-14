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
	Count               int64
	Version             int64
	ReactionRowCount    int64
	LikedReactionCount  int64
	MaxReactionVersion  int64
	InvalidVersionCount int64
}

type likeStateMaintenanceReactionAggregate struct {
	PostID              uint  `gorm:"column:post_id"`
	ReactionRowCount    int64 `gorm:"column:reaction_row_count"`
	LikedReactionCount  int64 `gorm:"column:liked_reaction_count"`
	MaxReactionVersion  int64 `gorm:"column:max_reaction_version"`
	InvalidVersionCount int64 `gorm:"column:invalid_version_count"`
}

const likeStateMaintenanceMemberScanBatch int64 = 1024

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
		membershipEqual, err := verifyLikeMembership(ctx, db, store, postID)
		if err != nil {
			return err
		}
		if !membershipEqual {
			log.Printf("[LikeStateMaintenance] like_state_expiry_mismatch post=%d reason=membership_not_equal", postID)
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
	activeIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		activeIDs = append(activeIDs, post.ID)
		result[post.ID] = likeStateMaintenanceBaseline{Count: post.LikeCount, Version: post.LikeSyncVersion}
	}
	var aggregates []likeStateMaintenanceReactionAggregate
	if err := db.Model(&models.PostReaction{}).
		Select("post_id, COUNT(*) AS reaction_row_count, COUNT(*) FILTER (WHERE liked = TRUE) AS liked_reaction_count, COALESCE(MAX(reaction_version), 0) AS max_reaction_version, COUNT(*) FILTER (WHERE reaction_version <= 0) AS invalid_version_count").
		Where("post_id IN ? AND reaction = ?", activeIDs, models.PostReactionLike).
		Group("post_id").
		Scan(&aggregates).Error; err != nil {
		return nil, err
	}
	for _, aggregate := range aggregates {
		baseline, ok := result[aggregate.PostID]
		if !ok {
			continue
		}
		baseline.ReactionRowCount = aggregate.ReactionRowCount
		baseline.LikedReactionCount = aggregate.LikedReactionCount
		baseline.MaxReactionVersion = aggregate.MaxReactionVersion
		baseline.InvalidVersionCount = aggregate.InvalidVersionCount
		result[aggregate.PostID] = baseline
	}
	return result, nil
}

func validateLikeStateMaintenanceBaseline(baseline likeStateMaintenanceBaseline) error {
	if baseline.Count < 0 || baseline.Version < 0 || baseline.ReactionRowCount < 0 ||
		baseline.LikedReactionCount < 0 || baseline.MaxReactionVersion < 0 || baseline.InvalidVersionCount < 0 ||
		baseline.LikedReactionCount > baseline.ReactionRowCount {
		return likes.ErrLikeProjectionNotReady
	}
	if baseline.ReactionRowCount == 0 {
		if baseline.Count != 0 || baseline.Version != 0 || baseline.LikedReactionCount != 0 || baseline.MaxReactionVersion != 0 || baseline.InvalidVersionCount != 0 {
			return likes.ErrLikeProjectionNotReady
		}
		return nil
	}
	if baseline.Version <= 0 || baseline.InvalidVersionCount != 0 || baseline.MaxReactionVersion != baseline.Version || baseline.LikedReactionCount != baseline.Count {
		return likes.ErrLikeProjectionNotReady
	}
	return nil
}

func verifyLikeMembership(ctx context.Context, db *gorm.DB, store *likes.Store, postID uint) (bool, error) {
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	if store == nil {
		return false, errors.New("like state store is not initialized")
	}
	if postID == 0 {
		return false, errors.New("invalid Like membership verification arguments")
	}
	// Count equality is checked before this walk. Do not derive it from SSCAN
	// results because a cursor iteration may return duplicate members.
	var cursor uint64
	for {
		userIDs, nextCursor, err := store.ScanUsers(ctx, postID, cursor, likeStateMaintenanceMemberScanBatch)
		if err != nil {
			return false, err
		}
		if len(userIDs) > 0 {
			var durableUserIDs []uint
			if err := db.Model(&models.PostReaction{}).
				Where("post_id = ? AND reaction = ? AND liked = ? AND user_id IN ?", postID, models.PostReactionLike, true, userIDs).
				Pluck("user_id", &durableUserIDs).Error; err != nil {
				return false, err
			}
			durableSet := make(map[uint]struct{}, len(durableUserIDs))
			for _, userID := range durableUserIDs {
				durableSet[userID] = struct{}{}
			}
			for _, userID := range userIDs {
				if _, ok := durableSet[userID]; !ok {
					return false, nil
				}
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			return true, nil
		}
	}
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
