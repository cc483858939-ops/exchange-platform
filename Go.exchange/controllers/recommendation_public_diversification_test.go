package controllers

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func publicDiversificationTestCandidates(count int) []hydratedRecommendationCandidate {
	result := make([]hydratedRecommendationCandidate, 0, count)
	for index := 1; index <= count; index++ {
		result = append(result, hydratedRecommendationCandidate{
			Candidate: embeddingCandidate{PostID: uint(index)},
			Post:      models.Post{Model: gorm.Model{ID: uint(index)}, AuthorID: uint(index)},
			Breakdown: recommendationScoreBreakdown{BaseScore: float64(count - index + 1)},
		})
	}
	return result
}

func publicDiversificationIDs(candidates []hydratedRecommendationCandidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Post.ID)
	}
	return ids
}

func TestDiversifyPublicRecommendationCandidatesIsDeterministicForSameRequest(t *testing.T) {
	ranked := publicDiversificationTestCandidates(100)
	first := diversifyPublicRecommendationCandidates(ranked, 20, "request-a")
	second := diversifyPublicRecommendationCandidates(ranked, 20, "request-a")

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same request produced different candidates: first=%v second=%v", publicDiversificationIDs(first), publicDiversificationIDs(second))
	}
	if len(first) > 40 {
		t.Fatalf("candidate subset length=%d, want <=40", len(first))
	}
}

func TestDiversifyPublicRecommendationCandidatesChangesAcrossRequests(t *testing.T) {
	ranked := publicDiversificationTestCandidates(100)
	first := diversifyPublicRecommendationCandidates(ranked, 20, "request-a")
	second := diversifyPublicRecommendationCandidates(ranked, 20, "request-b")

	if reflect.DeepEqual(publicDiversificationIDs(first), publicDiversificationIDs(second)) {
		t.Fatalf("different requests produced identical candidate order: %v", publicDiversificationIDs(first))
	}
}

func TestDiversifyPublicRecommendationCandidatesFavorsHighRankedCandidates(t *testing.T) {
	ranked := publicDiversificationTestCandidates(100)
	highRankCount := 0
	lowEligibleRankCount := 0
	for seed := 0; seed < 128; seed++ {
		selected := diversifyPublicRecommendationCandidates(ranked, 20, fmt.Sprintf("request-%d", seed))
		for _, candidate := range selected {
			if candidate.Post.ID <= 10 {
				highRankCount++
			}
			if candidate.Post.ID >= 71 && candidate.Post.ID <= 80 {
				lowEligibleRankCount++
			}
		}
	}
	if highRankCount <= lowEligibleRankCount {
		t.Fatalf("high-ranked candidates were not favored within Top 80: high=%d low_eligible=%d", highRankCount, lowEligibleRankCount)
	}
}

func TestDiversifyPublicRecommendationCandidatesUsesRankedWindow(t *testing.T) {
	selected := diversifyPublicRecommendationCandidates(publicDiversificationTestCandidates(100), 20, "request-window")
	for _, candidate := range selected {
		if candidate.Post.ID < 1 || candidate.Post.ID > 80 {
			t.Fatalf("candidate %d escaped the Top 80 diversification window", candidate.Post.ID)
		}
	}
}

func TestDiversifyPublicRecommendationCandidatesDeduplicatesAndPreservesScores(t *testing.T) {
	ranked := publicDiversificationTestCandidates(100)
	ranked[1].Post.ID = ranked[0].Post.ID
	ranked[1].Breakdown.BaseScore = 987
	selected := diversifyPublicRecommendationCandidates(ranked, 20, "request-dedup")

	seen := make(map[uint]struct{}, len(selected))
	for _, candidate := range selected {
		if _, exists := seen[candidate.Post.ID]; exists {
			t.Fatalf("duplicate post id %d in selected=%v", candidate.Post.ID, publicDiversificationIDs(selected))
		}
		seen[candidate.Post.ID] = struct{}{}
	}
	for _, candidate := range selected {
		if candidate.Post.ID == 1 && candidate.Breakdown.BaseScore != 100 {
			t.Fatalf("canonical score changed for post 1: %#v", candidate.Breakdown)
		}
	}
}

func TestDiversifyPublicRecommendationCandidatesKeepsSmallPoolsUnchanged(t *testing.T) {
	ranked := publicDiversificationTestCandidates(40)
	selected := diversifyPublicRecommendationCandidates(ranked, 20, "request-small")

	if !reflect.DeepEqual(selected, ranked) {
		t.Fatalf("small pool changed: got=%v want=%v", publicDiversificationIDs(selected), publicDiversificationIDs(ranked))
	}
}

func TestDiversifyPublicRecommendationCandidatesHandlesEmptyAndBoundsOutput(t *testing.T) {
	if got := diversifyPublicRecommendationCandidates(nil, 20, "request-empty"); len(got) != 0 {
		t.Fatalf("empty input returned %v", got)
	}
	ranked := publicDiversificationTestCandidates(500)
	selected := diversifyPublicRecommendationCandidates(ranked, 20, "request-bound")
	if len(selected) != 40 {
		t.Fatalf("large pool output length=%d want 40", len(selected))
	}
}

func publicServingTestFixture(t *testing.T, count int) (config.RecommendationConfig, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	fixture := publicDiversificationTestCandidates(count)
	for index := range fixture {
		fixture[index].Post.CreatedAt = now.Add(-time.Duration(index+1) * time.Minute)
		fixture[index].Post.UpdatedAt = fixture[index].Post.CreatedAt
		fixture[index].Post.LikeCount = int64(count - index)
	}

	originalLoader := publicRecommendationCandidateSetForServing
	originalHydrator := publicRecommendationHydrateForServing
	publicRecommendationCandidateSetForServing = func(_ time.Time, _ config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
		candidates := make([]embeddingCandidate, 0, len(fixture))
		for _, item := range fixture {
			if _, excluded := excluded[item.Post.ID]; excluded {
				continue
			}
			candidates = append(candidates, item.Candidate)
		}
		return recommendationCandidateSet{Candidates: candidates}, nil
	}
	publicRecommendationHydrateForServing = func(candidates []embeddingCandidate, _ time.Time) ([]hydratedRecommendationCandidate, error) {
		byID := make(map[uint]hydratedRecommendationCandidate, len(fixture))
		for _, item := range fixture {
			byID[item.Candidate.PostID] = item
		}
		hydrated := make([]hydratedRecommendationCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if item, ok := byID[candidate.PostID]; ok {
				hydrated = append(hydrated, item)
			}
		}
		return hydrated, nil
	}
	t.Cleanup(func() {
		publicRecommendationCandidateSetForServing = originalLoader
		publicRecommendationHydrateForServing = originalHydrator
	})

	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = false
	cfg.Exploration.Ratio = 0
	cfg.Exploration.MaxSlots = 0
	cfg.OutOfNetworkMinRatio = 0
	return cfg, now
}

func publicServingSelectedIDs(selected []selectedRecommendation) []uint {
	ids := make([]uint, 0, len(selected))
	for _, item := range selected {
		ids = append(ids, item.Post.ID)
	}
	return ids
}

func TestPublicRecommendationServingIsReproducibleForSameRequest(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 100)
	first, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-a", recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-a", recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected)) {
		t.Fatalf("same request produced different public results: first=%v second=%v", publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected))
	}
}

func TestPublicRecommendationServingVariesAcrossRequests(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 100)
	first, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-a", recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-b", recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected)) {
		t.Fatalf("different requests produced identical public results: %v", publicServingSelectedIDs(first.Selected))
	}
}

func TestPublicRecommendationServingKeepsExcludedPostsOut(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 100)
	excluded := map[uint]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}}
	outcome, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-exclusion", recommendationLanguageContext{}, excluded)
	if err != nil {
		t.Fatal(err)
	}
	for _, postID := range publicServingSelectedIDs(outcome.Selected) {
		if _, found := excluded[postID]; found {
			t.Fatalf("excluded post %d re-entered public serving result", postID)
		}
	}
}

func TestPublicRecommendationServingUsesGuestDiversification(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 100)
	originalDiversifier := diversifyPublicRecommendationCandidatesForServing
	calls := 0
	diversifyPublicRecommendationCandidatesForServing = func(ranked []hydratedRecommendationCandidate, limit int, requestID string) []hydratedRecommendationCandidate {
		calls++
		return originalDiversifier(ranked, limit, requestID)
	}
	t.Cleanup(func() {
		diversifyPublicRecommendationCandidatesForServing = originalDiversifier
	})

	if _, err := servePublicRecommendationCandidatePath(20, cfg, now, "guest-boundary", recommendationLanguageContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("public serving diversification calls=%d want 1", calls)
	}
}
