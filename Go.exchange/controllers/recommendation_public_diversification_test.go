package controllers

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func publicServingTestCandidates(count int) []hydratedRecommendationCandidate {
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

func publicServingTestFixture(t *testing.T, count int) (config.RecommendationConfig, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	fixture := publicServingTestCandidates(count)
	for index := range fixture {
		fixture[index].Post.CreatedAt = now.Add(-time.Duration(index+1) * time.Minute)
		fixture[index].Post.UpdatedAt = fixture[index].Post.CreatedAt
		fixture[index].Post.LikeCount = int64(count - index)
	}

	originalLoader := publicRecommendationCandidateSetForServing
	originalHydrator := publicRecommendationHydrateForServing
	publicRecommendationCandidateSetForServing = func(_ *gorm.DB, _ string, _ time.Time, _ config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
		candidates := make([]embeddingCandidate, 0, len(fixture))
		for _, item := range fixture {
			if _, excluded := excluded[item.Post.ID]; excluded {
				continue
			}
			candidates = append(candidates, item.Candidate)
		}
		return recommendationCandidateSet{Candidates: candidates}, nil
	}
	publicRecommendationHydrateForServing = func(_ *gorm.DB, _ string, candidates []embeddingCandidate, _ time.Time) ([]hydratedRecommendationCandidate, error) {
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
	first, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-a", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-a", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected)) {
		t.Fatalf("same request produced different public results: first=%v second=%v", publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected))
	}
}

func TestPublicRecommendationServingVariesAcrossRequests(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 100)
	first, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-a", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-b", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(publicServingSelectedIDs(first.Selected), publicServingSelectedIDs(second.Selected)) {
		t.Fatalf("different requests produced identical public results: %v", publicServingSelectedIDs(first.Selected))
	}
}

func TestPublicRecommendationServingKeepsHardServedPostsOut(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 300)
	served := make(map[uint]servedPost, 200)
	for postID := uint(1); postID <= 200; postID++ {
		served[postID] = servedPost{LastServedAt: now.Add(-5 * time.Minute), Hard: true}
	}
	outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-exclusion", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, served)
	if err != nil {
		t.Fatal(err)
	}
	for _, postID := range publicServingSelectedIDs(outcome.Selected) {
		if postID <= 200 {
			t.Fatalf("hard-served post %d re-entered public serving result", postID)
		}
	}
}

func TestPublicRecommendationServingFallbackPrefersUnseenOverHigherScoringSoftPosts(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 7)
	served := map[uint]servedPost{
		1: {LastServedAt: now.Add(-6 * 24 * time.Hour), Soft: true},
		2: {LastServedAt: now.Add(-4 * 24 * time.Hour), Soft: true},
		3: {LastServedAt: now.Add(-24 * time.Hour), Soft: true},
	}
	originalDiversifier := diversifyPublicRecommendationCandidatesForServing
	diversifyPublicRecommendationCandidatesForServing = func(ranked []hydratedRecommendationCandidate, _ int, _ string) []hydratedRecommendationCandidate {
		return ranked[:2]
	}
	t.Cleanup(func() {
		diversifyPublicRecommendationCandidatesForServing = originalDiversifier
	})

	outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 5, cfg, now, "guest-unseen-fallback", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, served)
	if err != nil {
		t.Fatal(err)
	}
	got := publicServingSelectedIDs(outcome.Selected)
	if !reflect.DeepEqual(got, []uint{4, 5, 6, 7, 1}) {
		t.Fatalf("unseen fallback order=%v want [4 5 6 7 1]", got)
	}
}

func TestPublicRecommendationServingFallbackPrefersUnseenThenOldestSoftServed(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 6)
	served := map[uint]servedPost{
		1: {LastServedAt: now.Add(-6 * 24 * time.Hour), Soft: true},
		2: {LastServedAt: now.Add(-4 * 24 * time.Hour), Soft: true},
		3: {LastServedAt: now.Add(-24 * time.Hour), Soft: true},
	}

	outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 5, cfg, now, "guest-fallback", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, served)
	if err != nil {
		t.Fatal(err)
	}
	got := publicServingSelectedIDs(outcome.Selected)
	if !reflect.DeepEqual(got, []uint{4, 5, 6, 1, 2}) {
		t.Fatalf("fallback order=%v want [4 5 6 1 2]", got)
	}
	for _, item := range outcome.Selected[3:] {
		if !item.Candidate.WasSoftServed {
			t.Fatalf("soft fallback item was not annotated: %#v", item)
		}
	}
}

func TestPublicRecommendationServingHardServedPostsRemainExcludedWhenPoolIsShort(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 6)
	served := map[uint]servedPost{
		1: {LastServedAt: now.Add(-5 * time.Minute), Hard: true},
	}

	outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 6, cfg, now, "guest-hard-short-pool", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, served)
	if err != nil {
		t.Fatal(err)
	}
	for _, postID := range publicServingSelectedIDs(outcome.Selected) {
		if postID == 1 {
			t.Fatalf("hard-served post was returned from fallback: %v", publicServingSelectedIDs(outcome.Selected))
		}
	}
}

func TestPublicRecommendationServingCanWalkPastFormerTwoHundredPostLimit(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 400)
	served := make(map[uint]servedPost)
	seen := make(map[uint]struct{})
	for page := 0; page < 15; page++ {
		outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, fmt.Sprintf("guest-page-%d", page), recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, served)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcome.Selected) != 20 {
			t.Fatalf("page=%d selected=%d want 20", page, len(outcome.Selected))
		}
		for _, postID := range publicServingSelectedIDs(outcome.Selected) {
			if _, duplicate := seen[postID]; duplicate {
				t.Fatalf("page=%d repeated post %d", page, postID)
			}
			seen[postID] = struct{}{}
			served[postID] = servedPost{LastServedAt: now, Hard: true}
		}
	}
	if len(seen) != 300 {
		t.Fatalf("unique posts=%d want 300", len(seen))
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

	if _, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 20, cfg, now, "guest-boundary", recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}, recommendationLanguageContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("public serving diversification calls=%d want 1", calls)
	}
}

func TestPublicRecommendationFallbackRetainsCapturedServingVersion(t *testing.T) {
	cfg, now := publicServingTestFixture(t, 2)
	originalCandidates := publicRecommendationCandidateSetForServing
	originalHydrate := publicRecommendationHydrateForServing
	currentServingVersion := "post_embedding_v1"
	var recallVersions []string
	var hydrationVersions []string
	publicRecommendationCandidateSetForServing = func(db *gorm.DB, servingVersion string, at time.Time, config config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
		recallVersions = append(recallVersions, servingVersion)
		if len(recallVersions) == 1 {
			currentServingVersion = "post_embedding_v2"
		}
		return originalCandidates(db, servingVersion, at, config, excluded)
	}
	publicRecommendationHydrateForServing = func(db *gorm.DB, servingVersion string, candidates []embeddingCandidate, at time.Time) ([]hydratedRecommendationCandidate, error) {
		hydrationVersions = append(hydrationVersions, servingVersion)
		return originalHydrate(db, servingVersion, candidates, at)
	}
	t.Cleanup(func() {
		publicRecommendationCandidateSetForServing = originalCandidates
		publicRecommendationHydrateForServing = originalHydrate
	})

	snapshot := recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"}
	outcome, err := servePublicRecommendationCandidatePath(context.Background(), &gorm.DB{}, 5, cfg, now, "guest-cutover", snapshot, recommendationLanguageContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if currentServingVersion != "post_embedding_v2" || len(recallVersions) != 2 || len(hydrationVersions) != 2 {
		t.Fatalf("runtime=%q recall=%v hydration=%v", currentServingVersion, recallVersions, hydrationVersions)
	}
	for _, version := range recallVersions {
		if version != snapshot.EmbeddingVersion {
			t.Fatalf("fallback recall versions=%v, want snapshot %q", recallVersions, snapshot.EmbeddingVersion)
		}
	}
	for _, version := range hydrationVersions {
		if version != snapshot.EmbeddingVersion {
			t.Fatalf("hydration versions=%v, want snapshot %q", hydrationVersions, snapshot.EmbeddingVersion)
		}
	}
	if outcome.EmbeddingVersion != snapshot.EmbeddingVersion {
		t.Fatalf("outcome serving version=%q want=%q", outcome.EmbeddingVersion, snapshot.EmbeddingVersion)
	}
}
