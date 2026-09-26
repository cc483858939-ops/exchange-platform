package controllers

import (
	"context"
	"errors"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"

	"github.com/go-redis/redis/v7"
)

const (
	userRecommendationHistoryPrefix = "recommendation:user:served:v1:"
	defaultUserServedHistoryLimit   = 1000
	defaultUserHardExclusionMinutes = 30
	defaultUserSoftLookbackDays     = 7
)

func userRecommendationHistoryKey(userID uint) string {
	return userRecommendationHistoryPrefix + strconv.FormatUint(uint64(userID), 10)
}

func effectiveUserServedHistoryLimit(cfg config.RecommendationConfig) int {
	if cfg.ServedHistoryLimit > 0 {
		return cfg.ServedHistoryLimit
	}
	return defaultUserServedHistoryLimit
}

func effectiveUserHardExclusionMinutes(cfg config.RecommendationConfig) int {
	if cfg.ServedHardExclusionMinutes > 0 {
		return cfg.ServedHardExclusionMinutes
	}
	return defaultUserHardExclusionMinutes
}

func effectiveUserSoftLookbackDays(cfg config.RecommendationConfig) int {
	if cfg.ServedSoftLookbackDays > 0 {
		return cfg.ServedSoftLookbackDays
	}
	return defaultUserSoftLookbackDays
}

func userRecommendationHistoryTTL(cfg config.RecommendationConfig) time.Duration {
	return time.Duration(effectiveUserSoftLookbackDays(cfg)+1) * 24 * time.Hour
}

func userServedHistoryWindows(now time.Time, cfg config.RecommendationConfig) (time.Time, time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return now.Add(-time.Duration(effectiveUserHardExclusionMinutes(cfg)) * time.Minute), now.AddDate(0, 0, -effectiveUserSoftLookbackDays(cfg))
}

func userHistoryWindow(now time.Time, cfg config.RecommendationConfig) recommendation.HistoryWindow {
	hardStart, softStart := userServedHistoryWindows(now, cfg)
	return recommendation.HistoryWindow{Now: now.UTC(), HardStart: hardStart, SoftStart: softStart, Limit: effectiveUserServedHistoryLimit(cfg), TTL: userRecommendationHistoryTTL(cfg)}
}

func hasRecommendationHistoryPostIDs(postIDs []uint) bool {
	for _, postID := range postIDs {
		if postID != 0 {
			return true
		}
	}
	return false
}

func classifyUserRecommendationServedAt(lastServedAt, now time.Time, cfg config.RecommendationConfig) (servedPost, bool) {
	hardStart, softStart := userServedHistoryWindows(now, cfg)
	lastServedAt = lastServedAt.UTC()
	if !lastServedAt.Before(hardStart) {
		return servedPost{LastServedAt: lastServedAt, Hard: true}, true
	}
	if lastServedAt.After(softStart) && lastServedAt.Before(hardStart) {
		return servedPost{LastServedAt: lastServedAt, Soft: true}, true
	}
	return servedPost{}, false
}

func userRecommendationHistoryMembers(postIDs []uint, score float64) []*redis.Z {
	seen := make(map[uint]struct{}, len(postIDs))
	members := make([]*redis.Z, 0, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		if _, exists := seen[postID]; exists {
			continue
		}
		seen[postID] = struct{}{}
		members = append(members, &redis.Z{Score: score, Member: strconv.FormatUint(uint64(postID), 10)})
	}
	return members
}

func loadUserRecommendationServedHistory(ctx context.Context, store recommendation.HistoryStore, userID uint, now time.Time, cfg config.RecommendationConfig) (recommendation.ServedHistory, error) {
	if userID == 0 {
		return recommendation.ServedHistory{}, nil
	}
	if store == nil {
		return nil, errors.New("redis is not initialized")
	}
	return store.LoadUserHistory(ctx, userID, userHistoryWindow(now, cfg))
}

func recordUserRecommendationServedPosts(ctx context.Context, store recommendation.HistoryStore, userID uint, postIDs []uint, now time.Time, cfg config.RecommendationConfig) error {
	if userID == 0 || !hasRecommendationHistoryPostIDs(postIDs) {
		return nil
	}
	if store == nil {
		return errors.New("redis is not initialized")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return store.RecordUserServed(ctx, userID, postIDs, userHistoryWindow(now, cfg))
}
