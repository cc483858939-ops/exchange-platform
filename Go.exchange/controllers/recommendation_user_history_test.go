package controllers

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestUserRecommendationHistoryKeyUsesStableUserID(t *testing.T) {
	key := userRecommendationHistoryKey(42)
	if want := userRecommendationHistoryPrefix + "42"; key != want {
		t.Fatalf("key=%q want %q", key, want)
	}
	if userRecommendationHistoryKey(42) != key {
		t.Fatal("same user ID did not produce a stable key")
	}
	if userRecommendationHistoryKey(43) == key {
		t.Fatal("different user IDs produced the same key")
	}
}

func TestUserRecommendationHistoryMembersDeduplicateAndIgnoreZeroIDs(t *testing.T) {
	members := userRecommendationHistoryMembers([]uint{0, 10, 10, 3, 0, 7}, 123)
	if len(members) != 3 {
		t.Fatalf("members=%d want 3", len(members))
	}
	for index, want := range []string{"10", "3", "7"} {
		if members[index].Member != want || members[index].Score != 123 {
			t.Fatalf("member[%d]=%#v want %s@123", index, members[index], want)
		}
	}
}

func TestClassifyUserRecommendationServedAtUsesExclusiveSoftBoundary(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := config.RecommendationConfig{
		ServedHardExclusionMinutes: 30,
		ServedSoftLookbackDays:     7,
	}

	hard, active := classifyUserRecommendationServedAt(now.Add(-29*time.Minute), now, cfg)
	if !active || !hard.Hard || hard.Soft {
		t.Fatalf("hard classification=%#v active=%t", hard, active)
	}

	hardAtBoundary, active := classifyUserRecommendationServedAt(now.Add(-30*time.Minute), now, cfg)
	if !active || !hardAtBoundary.Hard || hardAtBoundary.Soft {
		t.Fatalf("hard boundary classification=%#v active=%t", hardAtBoundary, active)
	}

	soft, active := classifyUserRecommendationServedAt(now.Add(-2*time.Hour), now, cfg)
	if !active || soft.Hard || !soft.Soft {
		t.Fatalf("soft classification=%#v active=%t", soft, active)
	}

	softAtBoundary, active := classifyUserRecommendationServedAt(now.AddDate(0, 0, -7), now, cfg)
	if active || softAtBoundary != (servedPost{}) {
		t.Fatalf("soft boundary classification=%#v active=%t", softAtBoundary, active)
	}

	if _, active := classifyUserRecommendationServedAt(now.AddDate(0, 0, -7).Add(-time.Nanosecond), now, cfg); active {
		t.Fatal("history older than the soft lookback remained active")
	}
}

func TestUserRecommendationHistoryTTLUsesSoftLookbackPlusGrace(t *testing.T) {
	for _, days := range []int{1, 7, 30} {
		cfg := config.RecommendationConfig{ServedSoftLookbackDays: days}
		if got, want := userRecommendationHistoryTTL(cfg), time.Duration(days+1)*24*time.Hour; got != want {
			t.Fatalf("soft lookback days=%d ttl=%s want %s", days, got, want)
		}
	}
	if got, want := userRecommendationHistoryTTL(config.RecommendationConfig{}), 8*24*time.Hour; got != want {
		t.Fatalf("default ttl=%s want %s", got, want)
	}
}

func TestUserRecommendationServedHistoryZeroUserIsNoOp(t *testing.T) {
	history, err := loadUserRecommendationServedHistory(context.Background(), 0, time.Time{}, config.RecommendationConfig{})
	if err != nil {
		t.Fatalf("load zero user: %v", err)
	}
	if history == nil || len(history) != 0 {
		t.Fatalf("zero user history=%#v want empty map", history)
	}
	if err := recordUserRecommendationServedPosts(context.Background(), 0, []uint{101}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record zero user: %v", err)
	}
	if err := recordUserRecommendationServedPosts(context.Background(), 42, []uint{0, 0}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record zero post IDs: %v", err)
	}
}
