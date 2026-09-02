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
			{PostID: 1, FromRecent: true},
			{PostID: 2, FromFollowing: true},
		},
		[]embeddingCandidate{
			{PostID: 3, FromTrending: true},
			{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: 0.9, SemanticRank: 4, FusionScore: 0.3, SourceCount: 2},
		},
		[]embeddingCandidate{
			{PostID: 2, FromTrending: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)},
		},
	)

	if len(merged) != 2 || merged[0].PostID != 1 || merged[1].PostID != 2 {
		t.Fatalf("merged=%#v, want IDs [1 2]", merged)
	}
	if !merged[0].FromRecent || !merged[0].FromSemantic || merged[0].PositiveSemanticSimilarity != 0.9 || merged[0].SemanticRank != 4 || merged[0].FusionScore != 0.3 || merged[0].SourceCount != 2 {
		t.Fatalf("article 1 metadata=%#v", merged[0])
	}
	if !merged[1].FromFollowing || !merged[1].FromTrending || !merged[1].WasSoftServed || !merged[1].LastServedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("article 2 metadata=%#v", merged[1])
	}
}

func TestMergeEmbeddingCandidatesMergesFusionMetadataAsSetMetadata(t *testing.T) {
	merged := mergeEmbeddingCandidates(1,
		[]embeddingCandidate{{PostID: 1, SemanticRank: 10, FollowingRank: 4, FusionScore: .2, SourceCount: 3}},
		[]embeddingCandidate{{PostID: 1, SemanticRank: 4, FollowingRank: 0, RecentRank: 8, FusionScore: .5, SourceCount: 2}},
	)
	if len(merged) != 1 {
		t.Fatalf("merged=%#v, want one candidate", merged)
	}
	if merged[0].SemanticRank != 4 || merged[0].FollowingRank != 4 || merged[0].RecentRank != 8 || merged[0].FusionScore != .5 || merged[0].SourceCount != 3 {
		t.Fatalf("merged fusion metadata=%#v, want min ranks, max score, max source count", merged[0])
	}
}

func TestMergeEmbeddingCandidatesPreservesSourceFlagsAndCap(t *testing.T) {
	merged := mergeEmbeddingCandidates(4,
		[]embeddingCandidate{{PostID: 1, PositiveSemanticSimilarity: .9, FromSemantic: true}, {PostID: 2, FromSemantic: true}},
		[]embeddingCandidate{{PostID: 1, FromRecent: true}, {PostID: 3, FromRecent: true}},
		[]embeddingCandidate{{PostID: 2, FromTrending: true}},
	)
	if len(merged) != 3 || merged[0].PostID != 1 || !merged[0].FromSemantic || !merged[0].FromRecent || merged[1].PostID != 2 || !merged[1].FromTrending {
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
		{cap: 200, ratio: 0.85, wantRecent: 170, wantEvergreen: 30},
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
