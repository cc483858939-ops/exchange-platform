package controllers

import "testing"

func TestRecommendationCandidateCapsUsesPersonalizedAndColdStart(t *testing.T) {
	cfg := defaultRecommendationConfig()
	cfg.Candidates.Personalized.Merged = 2
	cfg.Candidates.ColdStart.Merged = 3
	if got := recommendationCandidateCaps(userInterestProfile{PositiveVector: []float32{1, 0}}, cfg).Merged; got != 2 {
		t.Fatalf("personalized merged cap=%d, want 2", got)
	}
	if got := recommendationCandidateCaps(userInterestProfile{}, cfg).Merged; got != 3 {
		t.Fatalf("cold-start merged cap=%d, want 3", got)
	}
}

func TestMergeCandidateSetsUsesProvidedMergedLimit(t *testing.T) {
	result := mergeCandidateSets(
		recommendationCandidateSet{
			Candidates:     []embeddingCandidate{{PostID: 1}, {PostID: 2}},
			SemanticCount:  2,
			FollowingCount: 1,
		},
		recommendationCandidateSet{
			Candidates:    []embeddingCandidate{{PostID: 3}, {PostID: 4}},
			RecentCount:   2,
			TrendingCount: 1,
		},
		3,
	)
	if len(result.Candidates) != 3 || result.Candidates[0].PostID != 1 || result.Candidates[1].PostID != 2 || result.Candidates[2].PostID != 3 {
		t.Fatalf("merged candidates=%#v, want IDs [1 2 3]", result.Candidates)
	}
	if result.SemanticCount != 2 || result.FollowingCount != 1 || result.RecentCount != 2 || result.TrendingCount != 1 {
		t.Fatalf("source counts=%#v", result)
	}
}
