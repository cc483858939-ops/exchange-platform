package controllers

import (
	"context"
	"errors"
	"log"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type postLikeBaseline struct {
	Count            int64
	Version          int64
	ReactionRowCount int64
}

var (
	loadPostLikeBaselineFromDB = func(ctx context.Context, postID uint) (postLikeBaseline, error) {
		db := global.APIDb
		if db != nil {
			db = db.WithContext(ctx)
		}
		return loadActivePostLikeBaselineFromDB(db, postID)
	}
	loadPostLikeBaselinesFromDB = func(ctx context.Context, postIDs []uint) (map[uint]postLikeBaseline, error) {
		db := global.APIDb
		if db != nil {
			db = db.WithContext(ctx)
		}
		return loadPostLikeBaselinesFromDBWithDB(db, postIDs)
	}
)

var postLikeRecoveryGroup postLikeRecoveryFlights

func loadActivePostLikeBaselineFromDB(db *gorm.DB, postID uint) (postLikeBaseline, error) {
	if db == nil {
		return postLikeBaseline{}, errors.New("database is not initialized")
	}
	if postID == 0 {
		return postLikeBaseline{}, likes.ErrPostLikeUnavailable
	}
	var post models.Post
	if err := db.Unscoped().Model(&models.Post{}).
		Select("posts.id,posts.like_count,posts.like_sync_version,posts.deleted_at").
		Where("posts.id = ? AND posts.deleted_at IS NULL", postID).
		First(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return postLikeBaseline{}, likes.ErrPostLikeUnavailable
		}
		return postLikeBaseline{}, err
	}

	var reactionRows int64
	if err := db.Model(&models.PostReaction{}).
		Where("post_id = ? AND reaction = ?", postID, models.PostReactionLike).
		Count(&reactionRows).Error; err != nil {
		return postLikeBaseline{}, err
	}
	return postLikeBaseline{Count: post.LikeCount, Version: post.LikeSyncVersion, ReactionRowCount: reactionRows}, nil
}

func loadPostLikeBaselinesFromDBWithDB(db *gorm.DB, postIDs []uint) (map[uint]postLikeBaseline, error) {
	result := make(map[uint]postLikeBaseline, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}

	ids := uniquePostIDs(postIDs)
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
	postByID := make(map[uint]models.Post, len(posts))
	for _, post := range posts {
		postByID[post.ID] = post
		activeIDs = append(activeIDs, post.ID)
	}

	type reactionCount struct {
		PostID        uint
		ReactionCount int64
	}
	var reactionCounts []reactionCount
	if err := db.Model(&models.PostReaction{}).
		Select("post_id, COUNT(*) AS reaction_count").
		Where("post_id IN ? AND reaction = ?", activeIDs, models.PostReactionLike).
		Group("post_id").Scan(&reactionCounts).Error; err != nil {
		return nil, err
	}
	byPost := make(map[uint]int64, len(reactionCounts))
	for _, row := range reactionCounts {
		byPost[row.PostID] = row.ReactionCount
	}
	for postID, post := range postByID {
		result[postID] = postLikeBaseline{Count: post.LikeCount, Version: post.LikeSyncVersion, ReactionRowCount: byPost[postID]}
	}
	return result, nil
}

func validatePostLikeBaseline(baseline postLikeBaseline) error {
	if baseline.Count < 0 || baseline.Version < 0 || baseline.ReactionRowCount < 0 {
		return likes.ErrLikeProjectionNotReady
	}
	return nil
}

func classifyPostLikeRecovery(registered bool, marker *int64, baseline postLikeBaseline) (likes.RecoveryFence, error) {
	if err := validatePostLikeBaseline(baseline); err != nil {
		return likes.RecoveryFence{}, err
	}
	// SQL zero state, an absent registry entry, and an absent recoverable marker
	// do not prove Kafka has projected every prior Like. This function has no
	// trusted-new-Post creation evidence, so existing-Post recovery is unsafe.
	_ = registered
	_ = marker
	return likes.RecoveryFence{}, likes.ErrLikeRecoveryUnsafe
}

func ensurePostLikeStateReady(ctx context.Context, postID uint) error {
	_ = ctx
	metrics.RecordLikeLifecycleEvent("post_recovery_refused")
	log.Printf("[LikeLifecycle] like_state_post_recovery_refused post=%d", postID)
	return likes.ErrLikeRecoveryUnsafe
}

func logPostLikeRecoveryOutcome(postID uint, err error) {
	reason := "success"
	if err != nil {
		switch {
		case errors.Is(err, likes.ErrPostLikeUnavailable):
			reason = "unavailable"
		case errors.Is(err, likes.ErrLikeProjectionNotReady):
			reason = "projection_not_ready"
		case errors.Is(err, likes.ErrLikeRecoveryUnsafe):
			reason = "unsafe"
		case errors.Is(err, likes.ErrLikeRecoveryFenceLost):
			reason = "fence_lost"
		default:
			reason = "error"
		}
	}
	log.Printf("[LikeRecovery] like_state_rehydrate_%s post=%d", reason, postID)
}

func isPostLikeBatchUnavailableError(err error) bool {
	return errors.Is(err, likes.ErrPostLikeUnavailable) ||
		errors.Is(err, likes.ErrLikeProjectionNotReady) ||
		errors.Is(err, likes.ErrLikeRecoveryUnsafe) ||
		errors.Is(err, likes.ErrLikeRecoveryFenceLost) ||
		errors.Is(err, likes.ErrNotReady)
}

func setPostLikedStateWithRecovery(ctx context.Context, userID, postID uint, liked bool) (postLikeMutationResult, error) {
	result, err := setPostLikedStateWithRedis(ctx, userID, postID, liked)
	recordLikeReadinessEvent(userID, postID, err)
	return result, err
}

func loadPostLikeStateWithRecovery(ctx context.Context, userID, postID uint) (postLikeStateResult, error) {
	result, err := loadPostLikeStateFromRedis(ctx, userID, postID)
	recordLikeReadinessEvent(userID, postID, err)
	return result, err
}

func loadPostLikeStatesWithRecovery(ctx context.Context, userID uint, postIDs []uint) (postLikeStatesLoadResult, error) {
	result, err := loadPostLikeStatesFromRedis(ctx, userID, postIDs)
	if err != nil {
		postID := uint(0)
		if len(postIDs) > 0 {
			postID = postIDs[0]
		}
		recordLikeReadinessEvent(userID, postID, err)
	}
	return result, err
}

func recordLikeReadinessEvent(userID, postID uint, err error) {
	if errors.Is(err, likes.ErrUserLikeNotReady) {
		metrics.RecordLikeLifecycleEvent("user_not_ready")
		log.Printf("[LikeLifecycle] like_state_user_not_ready user=%d post=%d", userID, postID)
	}
	if errors.Is(err, likes.ErrPostLikeNotReady) {
		metrics.RecordLikeLifecycleEvent("post_not_ready")
		metrics.RecordLikeLifecycleEvent("post_recovery_refused")
		log.Printf("[LikeLifecycle] like_state_post_recovery_refused user=%d post=%d", userID, postID)
	}
	if errors.Is(err, likes.ErrLikeRedisType) {
		metrics.RecordLikeLifecycleEvent("redis_type_error")
		log.Printf("[LikeLifecycle] like_state_redis_type_error user=%d post=%d", userID, postID)
	}
	if errors.Is(err, likes.ErrLikeCountInconsistent) {
		metrics.RecordLikeLifecycleEvent("count_inconsistent")
		log.Printf("[LikeLifecycle] like_state_count_inconsistent user=%d post=%d", userID, postID)
	}
}

func uniquePostIDs(postIDs []uint) []uint {
	seen := make(map[uint]struct{}, len(postIDs))
	result := make([]uint, 0, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		if _, exists := seen[postID]; exists {
			continue
		}
		seen[postID] = struct{}{}
		result = append(result, postID)
	}
	return result
}
