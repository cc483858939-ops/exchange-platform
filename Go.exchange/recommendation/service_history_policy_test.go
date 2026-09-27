package recommendation

import (
	"context"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestRecommendationServiceUsesConfiguredHistoryPolicies(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := serviceTestConfig()
	cfg.ServedHistoryLimit = 11
	cfg.GuestServedHistoryLimit = 13
	cfg.ServedHardExclusionMinutes = 15
	cfg.ServedSoftLookbackDays = 3
	cfg.GuestServedHistoryTTLHours = 48

	wantUserWindow := HistoryWindow{
		Now: now, HardStart: now.Add(-15 * time.Minute), SoftStart: now.AddDate(0, 0, -3),
		Limit: 11, TTL: 4 * 24 * time.Hour,
	}
	wantGuestWindow := HistoryWindow{
		Now: now, HardStart: now.Add(-15 * time.Minute), SoftStart: now.AddDate(0, 0, -3),
		Limit: 13, TTL: 48 * time.Hour,
	}
	if got := userHistoryWindow(now, cfg); got != wantUserWindow {
		t.Fatalf("user history window=%#v want %#v", got, wantUserWindow)
	}
	if got := guestHistoryWindow(now, cfg); got != wantGuestWindow {
		t.Fatalf("guest history window=%#v want %#v", got, wantGuestWindow)
	}

	userCandidate := testCandidate(1, CandidateSourceRecent)
	guestCandidate := testCandidate(2, CandidateSourceRecent)
	posts := map[uint]models.Post{
		1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 101},
		2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 102},
	}
	calls := []string{}
	history := &serviceTestHistoryStore{calls: &calls}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: &serviceTestCandidateRepository{
				calls: &calls, recent: []Candidate{userCandidate}, publicRecent: []Candidate{guestCandidate}, posts: posts,
			},
			Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
			History:  history,
		},
		ServingVersions: serviceTestVersionProvider{version: "v1"},
	}, ServiceConfig{Recommendation: cfg})

	userRequestID := uuid.NewString()
	userResult, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: userRequestID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(userResult.Selected) != 1 || !reflect.DeepEqual(history.lastUserIDs, []uint{1}) || history.lastUserWindow != wantUserWindow {
		t.Fatalf("user history record ids=%v window=%#v result=%#v", history.lastUserIDs, history.lastUserWindow, userResult)
	}

	guestResult, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerGuest, GuestSessionID: "guest-session"}, Limit: 1, RequestID: "guest-request", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(guestResult.Selected) != 1 || !reflect.DeepEqual(history.lastGuestIDs, []uint{2}) || history.lastGuestWindow != wantGuestWindow {
		t.Fatalf("guest history record ids=%v window=%#v result=%#v", history.lastGuestIDs, history.lastGuestWindow, guestResult)
	}

	before := len(calls)
	if _, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerGuest}, Limit: 1, RequestID: "guest-without-session", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls[before:] {
		if call == "history_load_guest" || call == "history_record_guest" {
			t.Fatalf("empty guest session reached history store: calls=%v", calls[before:])
		}
	}
	if _, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated}, Now: now}); err == nil {
		t.Fatal("zero-user authenticated request was accepted")
	}
}

func TestRecommendationHistoryWindowsUseDefaults(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	user := userHistoryWindow(now, config.RecommendationConfig{})
	guest := guestHistoryWindow(now, config.RecommendationConfig{})
	wantNow := now.UTC()
	if user.Now != wantNow || user.HardStart != wantNow.Add(-30*time.Minute) || user.SoftStart != wantNow.AddDate(0, 0, -7) || user.Limit != 1000 || user.TTL != 8*24*time.Hour {
		t.Fatalf("default user history window=%#v", user)
	}
	if guest.Now != wantNow || guest.HardStart != wantNow.Add(-30*time.Minute) || guest.SoftStart != wantNow.AddDate(0, 0, -7) || guest.Limit != 2000 || guest.TTL != 24*time.Hour {
		t.Fatalf("default guest history window=%#v", guest)
	}
	hard, active := ClassifyServedAt(1, wantNow.Add(-30*time.Minute), user)
	if !active || !hard.Hard || hard.Soft {
		t.Fatalf("hard boundary classification=%#v active=%t", hard, active)
	}
	soft, active := ClassifyServedAt(2, wantNow.Add(-time.Hour), user)
	if !active || soft.Hard || !soft.Soft {
		t.Fatalf("soft classification=%#v active=%t", soft, active)
	}
	if _, active := ClassifyServedAt(3, user.SoftStart.Add(-time.Nanosecond), user); active {
		t.Fatal("history older than the soft lookback remained active")
	}
}
