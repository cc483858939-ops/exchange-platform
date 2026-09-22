package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

const (
	guestRecommendationSessionHeader = "X-Guest-Recommendation-Session"
	guestRecommendationHistoryPrefix = "recommendation:guest:served:v1:"
	defaultGuestServedHistoryLimit   = 2000
	defaultGuestHardExclusionMinutes = 30
	defaultGuestSoftLookbackDays     = 7
)

func parseGuestRecommendationSessionID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return "", false
	}
	return parsed.String(), true
}

func guestRecommendationHistoryKey(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return guestRecommendationHistoryPrefix + hex.EncodeToString(digest[:])
}

func effectiveGuestServedHistoryLimit(cfg config.RecommendationConfig) int {
	if cfg.GuestServedHistoryLimit > 0 {
		return cfg.GuestServedHistoryLimit
	}
	return defaultGuestServedHistoryLimit
}

func effectiveGuestHardExclusionMinutes(cfg config.RecommendationConfig) int {
	if cfg.ServedHardExclusionMinutes > 0 {
		return cfg.ServedHardExclusionMinutes
	}
	return defaultGuestHardExclusionMinutes
}

func effectiveGuestSoftLookbackDays(cfg config.RecommendationConfig) int {
	if cfg.ServedSoftLookbackDays > 0 {
		return cfg.ServedSoftLookbackDays
	}
	return defaultGuestSoftLookbackDays
}

func guestRecommendationHistoryTTL(cfg config.RecommendationConfig) time.Duration {
	return time.Duration(effectiveGuestSoftLookbackDays(cfg)+1) * 24 * time.Hour
}

func guestRecommendationHistoryMembers(postIDs []uint, score float64) []*redis.Z {
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

func guestServedHistoryWindows(now time.Time, cfg config.RecommendationConfig) (time.Time, time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	hardStart := now.Add(-time.Duration(effectiveGuestHardExclusionMinutes(cfg)) * time.Minute)
	softStart := now.AddDate(0, 0, -effectiveGuestSoftLookbackDays(cfg))
	return hardStart, softStart
}

func classifyGuestRecommendationServedAt(lastServedAt, now time.Time, cfg config.RecommendationConfig) (servedPost, bool) {
	hardStart, softStart := guestServedHistoryWindows(now, cfg)
	lastServedAt = lastServedAt.UTC()
	if !lastServedAt.Before(hardStart) {
		return servedPost{LastServedAt: lastServedAt, Hard: true}, true
	}
	if !lastServedAt.Before(softStart) {
		return servedPost{LastServedAt: lastServedAt, Soft: true}, true
	}
	return servedPost{}, false
}

func loadGuestRecommendationServedHistory(ctx context.Context, sessionID string, now time.Time, cfg config.RecommendationConfig) (map[uint]servedPost, error) {
	history := make(map[uint]servedPost)
	if strings.TrimSpace(sessionID) == "" {
		return history, nil
	}
	if global.RedisDB == nil {
		return nil, errors.New("redis is not initialized")
	}

	_, softStart := guestServedHistoryWindows(now, cfg)
	limit := effectiveGuestServedHistoryLimit(cfg)
	entries, err := global.RedisDB.WithContext(ctx).ZRevRangeByScoreWithScores(
		guestRecommendationHistoryKey(sessionID),
		&redis.ZRangeBy{
			Min:   strconv.FormatInt(softStart.Unix(), 10),
			Max:   "+inf",
			Count: int64(limit),
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
		classified, active := classifyGuestRecommendationServedAt(lastServedAt, now, cfg)
		if active {
			history[uint(postID)] = classified
		}
	}
	return history, nil
}

func recordGuestRecommendationServedPosts(ctx context.Context, sessionID string, postIDs []uint, now time.Time, cfg config.RecommendationConfig) error {
	if strings.TrimSpace(sessionID) == "" || len(postIDs) == 0 {
		return nil
	}
	if global.RedisDB == nil {
		return errors.New("redis is not initialized")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	members := guestRecommendationHistoryMembers(postIDs, float64(now.Unix()))
	if len(members) == 0 {
		return nil
	}

	key := guestRecommendationHistoryKey(sessionID)
	_, softStart := guestServedHistoryWindows(now, cfg)
	limit := int64(effectiveGuestServedHistoryLimit(cfg))
	ttl := guestRecommendationHistoryTTL(cfg)
	client := global.RedisDB.WithContext(ctx)
	pipe := client.TxPipeline()
	pipe.ZAdd(key, members...)
	pipe.ZRemRangeByScore(key, "-inf", "("+strconv.FormatInt(softStart.Unix(), 10))
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
