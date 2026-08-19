package controllers

import (
	"fmt"
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
			{ArticleID: 3, FromTrending: true},
			{ArticleID: 1, FromSemantic: true, PositiveSemanticSimilarity: 0.9},
		},
		[]embeddingCandidate{
			{ArticleID: 2, FromTrending: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)},
		},
	)

	if len(merged) != 2 || merged[0].ArticleID != 1 || merged[1].ArticleID != 2 {
		t.Fatalf("merged=%#v, want IDs [1 2]", merged)
	}
	if !merged[0].FromRecent || !merged[0].FromSemantic || merged[0].PositiveSemanticSimilarity != 0.9 {
		t.Fatalf("article 1 metadata=%#v", merged[0])
	}
	if !merged[1].FromFollowing || !merged[1].FromTrending || !merged[1].WasSoftServed || !merged[1].LastServedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("article 2 metadata=%#v", merged[1])
	}
}

func TestMergeEmbeddingCandidatesPreservesSourceFlagsAndCap(t *testing.T) {
	merged := mergeEmbeddingCandidates(4,
		[]embeddingCandidate{{ArticleID: 1, PositiveSemanticSimilarity: .9, FromSemantic: true}, {ArticleID: 2, FromSemantic: true}},
		[]embeddingCandidate{{ArticleID: 1, FromRecent: true}, {ArticleID: 3, FromRecent: true}},
		[]embeddingCandidate{{ArticleID: 2, FromTrending: true}},
	)
	if len(merged) != 3 || merged[0].ArticleID != 1 || !merged[0].FromSemantic || !merged[0].FromRecent || merged[1].ArticleID != 2 || !merged[1].FromTrending {
		t.Fatalf("merged=%#v", merged)
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

func TestRecommendationSemanticQuotaUsesReservedRecentAndEvergreenCapacity(t *testing.T) {
	tests := []struct {
		cap           int
		ratio         float64
		wantRecent    int
		wantEvergreen int
	}{
		{cap: 0, ratio: 0.8, wantRecent: 0, wantEvergreen: 0},
		{cap: 1, ratio: 0.8, wantRecent: 1, wantEvergreen: 0},
		{cap: 4, ratio: 0.75, wantRecent: 3, wantEvergreen: 1},
		{cap: 200, ratio: 0.8, wantRecent: 160, wantEvergreen: 40},
		{cap: 2, ratio: 0.01, wantRecent: 1, wantEvergreen: 1},
		{cap: 2, ratio: 0.99, wantRecent: 1, wantEvergreen: 1},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("cap_%d_ratio_%g", tc.cap, tc.ratio), func(t *testing.T) {
			recent, evergreen := recommendationSemanticQuota(tc.cap, tc.ratio)
			if recent != tc.wantRecent || evergreen != tc.wantEvergreen || recent+evergreen != tc.cap {
				t.Fatalf("quota=(%d,%d), want (%d,%d)", recent, evergreen, tc.wantRecent, tc.wantEvergreen)
			}
		})
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
			Candidates:    []embeddingCandidate{{ArticleID: 3}, {ArticleID: 4}},
			RecentCount:   2,
			TrendingCount: 1,
		},
		3,
	)
	if len(result.Candidates) != 3 || result.Candidates[0].ArticleID != 1 || result.Candidates[1].ArticleID != 2 || result.Candidates[2].ArticleID != 3 {
		t.Fatalf("merged candidates=%#v, want IDs [1 2 3]", result.Candidates)
	}
	if result.SemanticCount != 2 || result.FollowingCount != 1 || result.RecentCount != 2 || result.TrendingCount != 1 {
		t.Fatalf("source counts=%#v", result)
	}
}
