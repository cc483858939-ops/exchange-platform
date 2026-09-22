package controllers

import (
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestGuestRecommendationHistoryKeyHashesSessionID(t *testing.T) {
	sessionID := "4ca3706b-197e-4f63-8f51-f99176f8b61c"
	key := guestRecommendationHistoryKey(sessionID)

	if !strings.HasPrefix(key, guestRecommendationHistoryPrefix) {
		t.Fatalf("key=%q does not use the expected prefix", key)
	}
	if len(key) != len(guestRecommendationHistoryPrefix)+64 {
		t.Fatalf("key length=%d want %d", len(key), len(guestRecommendationHistoryPrefix)+64)
	}
	if strings.Contains(key, sessionID) {
		t.Fatalf("raw session ID leaked into Redis key: %q", key)
	}
	if key != guestRecommendationHistoryKey(sessionID) {
		t.Fatal("same session ID did not produce a stable key")
	}
	if key == guestRecommendationHistoryKey("4ca3706b-197e-4f63-8f51-f99176f8b62c") {
		t.Fatal("different session IDs produced the same key")
	}
}

func TestClassifyGuestRecommendationServedAtUsesHardAndInclusiveSoftBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := config.RecommendationConfig{
		ServedHardExclusionMinutes: 30,
		ServedSoftLookbackDays:     7,
	}

	hard, active := classifyGuestRecommendationServedAt(now.Add(-29*time.Minute), now, cfg)
	if !active || !hard.Hard || hard.Soft {
		t.Fatalf("hard boundary classification=%#v active=%t", hard, active)
	}

	softAtHardBoundary, active := classifyGuestRecommendationServedAt(now.Add(-30*time.Minute), now, cfg)
	if !active || !softAtHardBoundary.Hard || softAtHardBoundary.Soft {
		t.Fatalf("hard boundary classification=%#v active=%t", softAtHardBoundary, active)
	}

	soft, active := classifyGuestRecommendationServedAt(now.Add(-2*time.Hour), now, cfg)
	if !active || soft.Hard || !soft.Soft {
		t.Fatalf("soft classification=%#v active=%t", soft, active)
	}

	softAtLookbackBoundary, active := classifyGuestRecommendationServedAt(now.AddDate(0, 0, -7), now, cfg)
	if !active || softAtLookbackBoundary.Hard || !softAtLookbackBoundary.Soft {
		t.Fatalf("soft boundary classification=%#v active=%t", softAtLookbackBoundary, active)
	}

	if _, active := classifyGuestRecommendationServedAt(now.AddDate(0, 0, -7).Add(-time.Nanosecond), now, cfg); active {
		t.Fatal("history older than the soft lookback remained active")
	}
}

func TestGuestRecommendationHistoryMembersDeduplicateAndIgnoreZeroIDs(t *testing.T) {
	members := guestRecommendationHistoryMembers([]uint{0, 10, 10, 3, 0, 7}, 123)
	if len(members) != 3 {
		t.Fatalf("members=%d want 3", len(members))
	}
	for index, want := range []string{"10", "3", "7"} {
		if members[index].Member != want || members[index].Score != 123 {
			t.Fatalf("member[%d]=%#v want %s@123", index, members[index], want)
		}
	}
}

func TestGuestRecommendationHistoryTTLIncludesOneDayBuffer(t *testing.T) {
	cfg := config.RecommendationConfig{ServedSoftLookbackDays: 7}
	if got, want := guestRecommendationHistoryTTL(cfg), 8*24*time.Hour; got != want {
		t.Fatalf("ttl=%s want %s", got, want)
	}
}
