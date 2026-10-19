package recommendation

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

func TestAuthenticatedRecallSourcesExcludeMutedAuthorsIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "mute-recall-viewer")
	mutedAuthor := newRecommendationCandidateIntegrationUser(t, db, "mute-recall-muted-author")
	visibleAuthor := newRecommendationCandidateIntegrationUser(t, db, "mute-recall-visible-author")
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	mutedPost := newRecommendationSemanticPost(t, db, mutedAuthor, "mute-recall-muted", now.Add(-time.Hour), []float32{1, 0})
	visiblePost := newRecommendationSemanticPost(t, db, visibleAuthor, "mute-recall-visible", now.Add(-2*time.Hour), []float32{1, 0})
	windowStart, windowEnd := config.RecommendationCandidateWindowBounds(now, config.DefaultRecommendationCandidateWindowDays)
	if err := db.Create(&[]models.UserFollow{
		{FollowerID: viewer.ID, FollowingID: mutedAuthor.ID},
		{FollowerID: viewer.ID, FollowingID: visibleAuthor.ID},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserMute{MuterID: viewer.ID, MutedID: mutedAuthor.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id IN ?", []uint{mutedPost.ID, visiblePost.ID}).Update("like_count", 4).Error; err != nil {
		t.Fatal(err)
	}

	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	base := CandidateQuery{
		UserID: viewer.ID, ServingVersion: recommendationRecallV3TestVersion,
		Now: now, CandidateWindowStart: windowStart, CandidateWindowEnd: windowEnd, Limit: 10, PositiveVector: []float32{1, 0},
		TrendingCutoff: now.Add(-30 * 24 * time.Hour), TrendingReplyFactor: 0.5, TrendingHalfLifeHours: 24,
	}
	assertOnlyVisible := func(name string, candidates []Candidate, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s recall: %v", name, err)
		}
		if len(candidates) != 1 || candidates[0].PostID != visiblePost.ID {
			t.Fatalf("%s candidates=%#v, want only visible post %d (muted %d)", name, candidates, visiblePost.ID, mutedPost.ID)
		}
	}

	semantic, err := repository.LoadSemanticCandidates(context.Background(), base)
	assertOnlyVisible("semantic", semantic, err)
	following, err := repository.LoadFollowingCandidates(context.Background(), base)
	assertOnlyVisible("following", following, err)
	recent, err := repository.LoadRecentCandidates(context.Background(), base)
	assertOnlyVisible("recent", recent, err)
	trending, err := repository.LoadTrendingCandidates(context.Background(), base)
	assertOnlyVisible("trending", trending, err)

	softFallbackInput := base
	softFallbackInput.PositiveVector = nil
	softFallbackInput.SoftOnly = true
	softFallbackInput.Served = map[uint]ServedItem{mutedPost.ID: {Soft: true}}
	softFallback, err := repository.LoadRecentCandidates(context.Background(), softFallbackInput)
	if err != nil || len(softFallback) != 0 {
		t.Fatalf("soft fallback candidates=%#v err=%v; muted post must not be restored", softFallback, err)
	}

	guest, err := repository.LoadPublicRecentCandidates(context.Background(), PublicCandidateQuery{
		ServingVersion: recommendationRecallV3TestVersion, Now: now,
		CandidateWindowStart: windowStart, CandidateWindowEnd: windowEnd, Limit: 10,
	})
	if err != nil {
		t.Fatalf("guest recent recall: %v", err)
	}
	guestIDs := make(map[uint]bool, len(guest))
	for _, candidate := range guest {
		guestIDs[candidate.PostID] = true
	}
	if !guestIDs[mutedPost.ID] || !guestIDs[visiblePost.ID] {
		t.Fatalf("guest candidates=%#v; public recommendations must ignore viewer mute relations", guest)
	}
}
