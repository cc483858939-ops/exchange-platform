package controllers

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"
)

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

func TestUserHistoryWindowUsesConfiguredPolicyAndDefaults(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	window := userHistoryWindow(now, config.RecommendationConfig{
		ServedHistoryLimit:         25,
		ServedHardExclusionMinutes: 12,
		ServedSoftLookbackDays:     2,
	})
	wantNow := now.UTC()
	want := recommendation.HistoryWindow{
		Now:       wantNow,
		HardStart: wantNow.Add(-12 * time.Minute),
		SoftStart: wantNow.AddDate(0, 0, -2),
		Limit:     25,
		TTL:       3 * 24 * time.Hour,
	}
	if window != want {
		t.Fatalf("window=%#v want %#v", window, want)
	}

	defaults := userHistoryWindow(wantNow, config.RecommendationConfig{})
	if defaults.Limit != defaultUserServedHistoryLimit || defaults.HardStart != wantNow.Add(-30*time.Minute) || defaults.SoftStart != wantNow.AddDate(0, 0, -7) || defaults.TTL != 8*24*time.Hour {
		t.Fatalf("default user history window=%#v", defaults)
	}
}

func TestUserRecommendationHistoryHelpersInvokeStore(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := config.RecommendationConfig{ServedHistoryLimit: 11, ServedHardExclusionMinutes: 15, ServedSoftLookbackDays: 3}
	wantHistory := recommendation.ServedHistory{42: {PostID: 42, Hard: true}}
	store := &recommendationHistoryStoreRecorder{userHistory: wantHistory}

	history, err := loadUserRecommendationServedHistory(context.Background(), store, 9, now, cfg)
	if err != nil {
		t.Fatalf("load user history: %v", err)
	}
	if store.userLoadCalls != 1 || store.lastUserID != 9 || store.lastUserWindow != userHistoryWindow(now, cfg) {
		t.Fatalf("user load call: calls=%d id=%d window=%#v", store.userLoadCalls, store.lastUserID, store.lastUserWindow)
	}
	if history[42] != wantHistory[42] {
		t.Fatalf("loaded history=%#v want %#v", history, wantHistory)
	}

	postIDs := []uint{4, 5}
	if err := recordUserRecommendationServedPosts(context.Background(), store, 9, postIDs, now, cfg); err != nil {
		t.Fatalf("record user history: %v", err)
	}
	if store.userRecordCalls != 1 || store.lastUserID != 9 || store.lastUserWindow != userHistoryWindow(now, cfg) || len(store.lastUserPostIDs) != len(postIDs) || store.lastUserPostIDs[0] != 4 || store.lastUserPostIDs[1] != 5 {
		t.Fatalf("user record call: calls=%d id=%d postIDs=%v window=%#v", store.userRecordCalls, store.lastUserID, store.lastUserPostIDs, store.lastUserWindow)
	}
}

func TestUserRecommendationServedHistoryZeroUserIsNoOp(t *testing.T) {
	store := &recommendationHistoryStoreRecorder{}
	history, err := loadUserRecommendationServedHistory(context.Background(), store, 0, time.Time{}, config.RecommendationConfig{})
	if err != nil {
		t.Fatalf("load zero user: %v", err)
	}
	if history == nil || len(history) != 0 {
		t.Fatalf("zero user history=%#v want empty map", history)
	}
	if err := recordUserRecommendationServedPosts(context.Background(), store, 0, []uint{101}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record zero user: %v", err)
	}
	if err := recordUserRecommendationServedPosts(context.Background(), store, 42, []uint{0, 0}, time.Time{}, config.RecommendationConfig{}); err != nil {
		t.Fatalf("record zero post IDs: %v", err)
	}
	if store.userLoadCalls != 0 || store.userRecordCalls != 0 {
		t.Fatalf("empty user history operations reached store: load=%d record=%d", store.userLoadCalls, store.userRecordCalls)
	}
}

type recommendationHistoryStoreRecorder struct {
	userLoadCalls    int
	userRecordCalls  int
	guestLoadCalls   int
	guestRecordCalls int
	lastUserID       uint
	lastGuestSession string
	lastUserPostIDs  []uint
	lastGuestPostIDs []uint
	lastUserWindow   recommendation.HistoryWindow
	lastGuestWindow  recommendation.HistoryWindow
	userHistory      recommendation.ServedHistory
	guestHistory     recommendation.ServedHistory
}

func (store *recommendationHistoryStoreRecorder) LoadUserHistory(_ context.Context, userID uint, window recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	store.userLoadCalls++
	store.lastUserID = userID
	store.lastUserWindow = window
	return store.userHistory, nil
}

func (store *recommendationHistoryStoreRecorder) RecordUserServed(_ context.Context, userID uint, postIDs []uint, window recommendation.HistoryWindow) error {
	store.userRecordCalls++
	store.lastUserID = userID
	store.lastUserPostIDs = append([]uint(nil), postIDs...)
	store.lastUserWindow = window
	return nil
}

func (store *recommendationHistoryStoreRecorder) LoadGuestHistory(_ context.Context, sessionID string, window recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	store.guestLoadCalls++
	store.lastGuestSession = sessionID
	store.lastGuestWindow = window
	return store.guestHistory, nil
}

func (store *recommendationHistoryStoreRecorder) RecordGuestServed(_ context.Context, sessionID string, postIDs []uint, window recommendation.HistoryWindow) error {
	store.guestRecordCalls++
	store.lastGuestSession = sessionID
	store.lastGuestPostIDs = append([]uint(nil), postIDs...)
	store.lastGuestWindow = window
	return nil
}
