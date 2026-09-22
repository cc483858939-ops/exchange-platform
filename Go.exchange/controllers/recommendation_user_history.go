package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

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
	hardStart := now.Add(-time.Duration(effectiveUserHardExclusionMinutes(cfg)) * time.Minute)
	softStart := now.AddDate(0, 0, -effectiveUserSoftLookbackDays(cfg))
	return hardStart, softStart
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
		members = append(members, &redis.Z{
			Score:  score,
			Member: strconv.FormatUint(uint64(postID), 10),
		})
	}
	return members
}

func loadUserRecommendationServedHistory(ctx context.Context, userID uint, now time.Time, cfg config.RecommendationConfig) (map[uint]servedPost, error) {
	history := make(map[uint]servedPost)
	if userID == 0 {
		return history, nil
	}
	if global.RedisDB == nil {
		return nil, errors.New("redis is not initialized")
	}

	_, softStart := userServedHistoryWindows(now, cfg)
	entries, err := global.RedisDB.WithContext(ctx).ZRevRangeByScoreWithScores(
		userRecommendationHistoryKey(userID),
		&redis.ZRangeBy{
			Min:   "(" + strconv.FormatInt(softStart.Unix(), 10),
			Max:   "+inf",
			Count: int64(effectiveUserServedHistoryLimit(cfg)),
		},
	).Result()
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		postID, err := strconv.ParseUint(strings.TrimSpace(fmt.Sprint(entry.Member)), 10, 64)
		if err != nil || postID == 0 || uint64(uint(postID)) != postID {
			continue
		}
		lastServedAt := time.Unix(int64(entry.Score), 0).UTC()
		classified, active := classifyUserRecommendationServedAt(lastServedAt, now, cfg)
		if active {
			history[uint(postID)] = classified
		}
	}
	return history, nil
}

func recordUserRecommendationServedPosts(ctx context.Context, userID uint, postIDs []uint, now time.Time, cfg config.RecommendationConfig) error {
	if userID == 0 || len(postIDs) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	members := userRecommendationHistoryMembers(postIDs, float64(now.Unix()))
	if len(members) == 0 {
		return nil
	}
	if global.RedisDB == nil {
		return errors.New("redis is not initialized")
	}

	key := userRecommendationHistoryKey(userID)
	_, softStart := userServedHistoryWindows(now, cfg)
	limit := int64(effectiveUserServedHistoryLimit(cfg))
	ttl := userRecommendationHistoryTTL(cfg)
	client := global.RedisDB.WithContext(ctx)
	pipe := client.TxPipeline()
	pipe.ZAdd(key, members...)
	pipe.ZRemRangeByScore(key, "-inf", strconv.FormatInt(softStart.Unix(), 10))
	cardinality := pipe.ZCard(key)
	pipe.Expire(key, ttl)
	if _, err := pipe.ExecContext(ctx); err != nil {
		return err
	}

	if cardinality.Val() > limit {
		if err := client.ZRemRangeByRank(key, 0, cardinality.Val()-limit-1).Err(); err != nil {
			return err
		}
		if err := client.Expire(key, ttl).Err(); err != nil {
			return err
		}
	}
	return nil
}
