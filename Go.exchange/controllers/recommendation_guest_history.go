package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
)

const (
	guestRecommendationSessionHeader  = "X-Guest-Recommendation-Session"
	defaultGuestServedHistoryLimit    = 2000
	defaultGuestHardExclusionMinutes  = 30
	defaultGuestSoftLookbackDays      = 7
	defaultGuestServedHistoryTTLHours = 24
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

func effectiveGuestServedHistoryTTLHours(cfg config.RecommendationConfig) int {
	if cfg.GuestServedHistoryTTLHours > 0 {
		return cfg.GuestServedHistoryTTLHours
	}
	return defaultGuestServedHistoryTTLHours
}

func guestRecommendationHistoryTTL(cfg config.RecommendationConfig) time.Duration {
	return time.Duration(effectiveGuestServedHistoryTTLHours(cfg)) * time.Hour
}

func guestHistoryWindow(now time.Time, cfg config.RecommendationConfig) recommendation.HistoryWindow {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return recommendation.HistoryWindow{
		Now:       now,
		HardStart: now.Add(-time.Duration(effectiveGuestHardExclusionMinutes(cfg)) * time.Minute),
		SoftStart: now.AddDate(0, 0, -effectiveGuestSoftLookbackDays(cfg)),
		Limit:     effectiveGuestServedHistoryLimit(cfg), TTL: guestRecommendationHistoryTTL(cfg),
	}
}

func guestServedHistoryWindows(now time.Time, cfg config.RecommendationConfig) (time.Time, time.Time) {
	window := guestHistoryWindow(now, cfg)
	return window.HardStart, window.SoftStart
}

func classifyGuestRecommendationServedAt(lastServedAt, now time.Time, cfg config.RecommendationConfig) (servedPost, bool) {
	window := guestHistoryWindow(now, cfg)
	return recommendation.ClassifyServedAt(0, lastServedAt, window)
}

func loadGuestRecommendationServedHistory(ctx context.Context, store recommendation.HistoryStore, sessionID string, now time.Time, cfg config.RecommendationConfig) (recommendation.ServedHistory, error) {
	if strings.TrimSpace(sessionID) == "" {
		return recommendation.ServedHistory{}, nil
	}
	if store == nil {
		return nil, errors.New("recommendation history store is not initialized")
	}
	return store.LoadGuestHistory(ctx, sessionID, guestHistoryWindow(now, cfg))
}

func recordGuestRecommendationServedPosts(ctx context.Context, store recommendation.HistoryStore, sessionID string, postIDs []uint, now time.Time, cfg config.RecommendationConfig) error {
	if strings.TrimSpace(sessionID) == "" || !hasRecommendationHistoryPostIDs(postIDs) {
		return nil
	}
	if store == nil {
		return errors.New("recommendation history store is not initialized")
	}
	return store.RecordGuestServed(ctx, sessionID, postIDs, guestHistoryWindow(now, cfg))
}
