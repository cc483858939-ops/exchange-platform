package controllers

import (
	"context"
	"errors"
	"log"

	"Go.exchange/global"
	"Go.exchange/likes"
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
	if marker != nil || registered || baseline.Count != 0 || baseline.Version != 0 || baseline.ReactionRowCount != 0 {
		return likes.RecoveryFence{}, likes.ErrLikeRecoveryUnsafe
	}
	return likes.RecoveryFence{AllowZeroBootstrap: true}, nil
}

func ensurePostLikeStateReady(ctx context.Context, postID uint) error {
	if postID == 0 {
		return likes.ErrPostLikeUnavailable
	}
	results, err := postLikeRecoveryGroup.recover(ctx, likes.NewStore(global.RedisDB), []uint{postID}, func(ctx context.Context, ids []uint) (map[uint]postLikeBaseline, error) {
		baseline, err := loadPostLikeBaselineFromDB(ctx, ids[0])
		if err != nil {
			return nil, err
		}
		return map[uint]postLikeBaseline{ids[0]: baseline}, nil
	})
	if err == nil {
		err = results[postID]
	}
	logPostLikeRecoveryOutcome(postID, err)
	return err
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
	if !errors.Is(err, likes.ErrNotReady) {
		return result, err
	}
	if recoveryErr := ensurePostLikeStateReady(ctx, postID); recoveryErr != nil && !errors.Is(recoveryErr, likes.ErrLikeRecoveryFenceLost) {
		return postLikeMutationResult{}, recoveryErr
	}
	return setPostLikedStateWithRedis(ctx, userID, postID, liked)
}

func loadPostLikeStateWithRecovery(ctx context.Context, userID, postID uint) (postLikeStateResult, error) {
	result, err := loadPostLikeStateFromRedis(ctx, userID, postID)
	if !errors.Is(err, likes.ErrNotReady) {
		return result, err
	}
	if recoveryErr := ensurePostLikeStateReady(ctx, postID); recoveryErr != nil && !errors.Is(recoveryErr, likes.ErrLikeRecoveryFenceLost) {
		return postLikeStateResult{}, recoveryErr
	}
	return loadPostLikeStateFromRedis(ctx, userID, postID)
}

func loadPostLikeStatesWithRecovery(ctx context.Context, userID uint, postIDs []uint) (postLikeStatesLoadResult, error) {
	result, err := loadPostLikeStatesFromRedis(ctx, userID, postIDs)
	if err != nil || len(result.Unavailable) == 0 {
		return result, err
	}

	recoveryResults, err := postLikeRecoveryGroup.recover(ctx, likes.NewStore(global.RedisDB), result.Unavailable, loadPostLikeBaselinesFromDB)
	if err != nil {
		return postLikeStatesLoadResult{}, err
	}

	unavailable := make(map[uint]struct{}, len(result.Unavailable))
	for _, postID := range result.Unavailable {
		recoverErr := recoveryResults[postID]
		if recoverErr != nil {
			if isPostLikeBatchUnavailableError(recoverErr) {
				unavailable[postID] = struct{}{}
				logPostLikeRecoveryOutcome(postID, recoverErr)
				continue
			}
			return postLikeStatesLoadResult{}, recoverErr
		}
		logPostLikeRecoveryOutcome(postID, nil)
	}

	recoveredIDs := make([]uint, 0, len(result.Unavailable)-len(unavailable))
	for _, postID := range result.Unavailable {
		if _, isUnavailable := unavailable[postID]; !isUnavailable {
			recoveredIDs = append(recoveredIDs, postID)
		}
	}
	if len(recoveredIDs) > 0 {
		readyResult, getErr := loadPostLikeStatesFromRedis(ctx, userID, recoveredIDs)
		if getErr != nil {
			return postLikeStatesLoadResult{}, getErr
		}
		for postID, state := range readyResult.States {
			result.States[postID] = state
		}
		for _, postID := range readyResult.Unavailable {
			unavailable[postID] = struct{}{}
		}
	}

	result.Unavailable = result.Unavailable[:0]
	for _, postID := range postIDs {
		if _, isUnavailable := unavailable[postID]; isUnavailable {
			result.Unavailable = append(result.Unavailable, postID)
		}
	}
	return result, nil
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
