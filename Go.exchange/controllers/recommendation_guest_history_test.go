package controllers

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"
)

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

func TestGuestRecommendationHistoryTTLDefaultsTo24HoursIndependentOfSoftLookback(t *testing.T) {
	for _, softLookbackDays := range []int{1, 7, 30} {
		cfg := config.RecommendationConfig{ServedSoftLookbackDays: softLookbackDays}
		if got, want := guestRecommendationHistoryTTL(cfg), 24*time.Hour; got != want {
			t.Fatalf("soft lookback days=%d ttl=%s want %s", softLookbackDays, got, want)
		}
	}
}

func TestGuestRecommendationHistoryTTLUsesConfiguredHours(t *testing.T) {
	cfg := config.RecommendationConfig{
		ServedSoftLookbackDays:     1,
		GuestServedHistoryTTLHours: 48,
	}
	if got, want := guestRecommendationHistoryTTL(cfg), 48*time.Hour; got != want {
		t.Fatalf("ttl=%s want %s", got, want)
	}
}

func TestGuestHistoryWindowUsesConfiguredPolicyAndDefaults(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("UTC-5", -5*60*60))
	window := guestHistoryWindow(now, config.RecommendationConfig{
		GuestServedHistoryLimit:    25,
		ServedHardExclusionMinutes: 12,
		ServedSoftLookbackDays:     2,
		GuestServedHistoryTTLHours: 36,
	})
	wantNow := now.UTC()
	want := recommendation.HistoryWindow{
		Now:       wantNow,
		HardStart: wantNow.Add(-12 * time.Minute),
		SoftStart: wantNow.AddDate(0, 0, -2),
		Limit:     25,
		TTL:       36 * time.Hour,
	}
	if window != want {
		t.Fatalf("window=%#v want %#v", window, want)
	}

	defaults := guestHistoryWindow(wantNow, config.RecommendationConfig{})
	if defaults.Limit != defaultGuestServedHistoryLimit || defaults.HardStart != wantNow.Add(-30*time.Minute) || defaults.SoftStart != wantNow.AddDate(0, 0, -7) || defaults.TTL != 24*time.Hour {
		t.Fatalf("default guest history window=%#v", defaults)
	}
}

func TestGuestRecommendationHistoryHelpersInvokeStore(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := config.RecommendationConfig{GuestServedHistoryLimit: 11, ServedHardExclusionMinutes: 15, ServedSoftLookbackDays: 3, GuestServedHistoryTTLHours: 48}
	wantHistory := recommendation.ServedHistory{42: {PostID: 42, Hard: true}}
	store := &recommendationHistoryStoreRecorder{guestHistory: wantHistory}
	sessionID := "guest-session"

	history, err := loadGuestRecommendationServedHistory(context.Background(), store, sessionID, now, cfg)
	if err != nil {
		t.Fatalf("load guest history: %v", err)
	}
	if store.guestLoadCalls != 1 || store.lastGuestSession != sessionID || store.lastGuestWindow != guestHistoryWindow(now, cfg) {
		t.Fatalf("guest load call: calls=%d session=%q window=%#v", store.guestLoadCalls, store.lastGuestSession, store.lastGuestWindow)
	}
	if history[42] != wantHistory[42] {
		t.Fatalf("loaded history=%#v want %#v", history, wantHistory)
	}

	postIDs := []uint{4, 5}
	if err := recordGuestRecommendationServedPosts(context.Background(), store, sessionID, postIDs, now, cfg); err != nil {
		t.Fatalf("record guest history: %v", err)
	}
	if store.guestRecordCalls != 1 || store.lastGuestSession != sessionID || store.lastGuestWindow != guestHistoryWindow(now, cfg) || len(store.lastGuestPostIDs) != len(postIDs) || store.lastGuestPostIDs[0] != 4 || store.lastGuestPostIDs[1] != 5 {
		t.Fatalf("guest record call: calls=%d session=%q postIDs=%v window=%#v", store.guestRecordCalls, store.lastGuestSession, store.lastGuestPostIDs, store.lastGuestWindow)
	}
}

func TestGuestRecommendationServedHistoryEmptyInputsAreNoOp(t *testing.T) {
	store := &recommendationHistoryStoreRecorder{}
	history, err := loadGuestRecommendationServedHistory(context.Background(), store, "  ", time.Time{}, config.RecommendationConfig{})
	if err != nil {
		t.Fatalf("load empty guest history: %v", err)
	}
	if history == nil || len(history) != 0 {
		t.Fatalf("empty guest history=%#v want empty map", history)
	}
	if err := recordGuestRecommendationServedPosts(context.Background(), store, "  ", []uint{101}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record empty guest session: %v", err)
	}
	if err := recordGuestRecommendationServedPosts(context.Background(), store, "guest", []uint{0, 0}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record zero post IDs: %v", err)
	}
	if store.guestLoadCalls != 0 || store.guestRecordCalls != 0 {
		t.Fatalf("empty guest history operations reached store: load=%d record=%d", store.guestLoadCalls, store.guestRecordCalls)
	}
}
