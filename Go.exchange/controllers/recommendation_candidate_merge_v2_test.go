package controllers

import (
	"testing"
	"time"
)

func TestMergeEmbeddingCandidatesContinuesMetadataAggregationAfterCap(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	merged := mergeEmbeddingCandidates(
		2,
		[]embeddingCandidate{
			{ArticleID: 1, FromRecent: true},
			{ArticleID: 2, FromFollowing: true},
		},
		[]embeddingCandidate{
			{ArticleID: 3, FromPopular: true},
			{ArticleID: 1, FromSemantic: true, SemanticSimilarity: 0.9},
		},
		[]embeddingCandidate{
			{ArticleID: 2, FromPopular: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)},
		},
	)

	if len(merged) != 2 || merged[0].ArticleID != 1 || merged[1].ArticleID != 2 {
		t.Fatalf("merged=%#v, want IDs [1 2]", merged)
	}
	if !merged[0].FromRecent || !merged[0].FromSemantic || merged[0].SemanticSimilarity != 0.9 {
		t.Fatalf("article 1 metadata=%#v", merged[0])
	}
	if !merged[1].FromFollowing || !merged[1].FromPopular || !merged[1].WasSoftServed || !merged[1].LastServedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("article 2 metadata=%#v", merged[1])
	}
}

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
			Candidates:     []embeddingCandidate{{ArticleID: 1}, {ArticleID: 2}},
			SemanticCount:  2,
			FollowingCount: 1,
		},
		recommendationCandidateSet{
			Candidates:   []embeddingCandidate{{ArticleID: 3}, {ArticleID: 4}},
			RecentCount:  2,
			PopularCount: 1,
		},
		3,
	)
	if len(result.Candidates) != 3 || result.Candidates[0].ArticleID != 1 || result.Candidates[1].ArticleID != 2 || result.Candidates[2].ArticleID != 3 {
		t.Fatalf("merged candidates=%#v, want IDs [1 2 3]", result.Candidates)
	}
	if result.SemanticCount != 2 || result.FollowingCount != 1 || result.RecentCount != 2 || result.PopularCount != 1 {
		t.Fatalf("source counts=%#v", result)
	}
}
