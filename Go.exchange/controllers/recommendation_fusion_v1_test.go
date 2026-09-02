package controllers

import (
	"math"
	"testing"
)

func recommendationFusionCandidateByID(candidates []embeddingCandidate, postID uint) (embeddingCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.PostID == postID {
			return candidate, true
		}
	}
	return embeddingCandidate{}, false
}

func TestRecommendationRRFFusionAggregatesMultipleSources(t *testing.T) {
	got := fuseRecommendationCandidates(
		10,
		60,
		recommendationRecallList{
			Source: recommendationRecallSourceSemantic,
			Candidates: []embeddingCandidate{
				{PostID: 1, PositiveSemanticSimilarity: .4},
				{PostID: 2, PositiveSemanticSimilarity: .83},
			},
		},
		recommendationRecallList{
			Source:     recommendationRecallSourceRecent,
			Candidates: []embeddingCandidate{{PostID: 2}, {PostID: 3}},
		},
	)

	candidate, ok := recommendationFusionCandidateByID(got, 2)
	if !ok {
		t.Fatalf("fused candidates=%#v, missing post 2", got)
	}
	wantScore := 1.0/(60+2) + 1.0/(60+1)
	if !candidate.FromSemantic || !candidate.FromRecent || candidate.SemanticRank != 2 || candidate.RecentRank != 1 || candidate.SourceCount != 2 {
		t.Fatalf("post 2 metadata=%#v", candidate)
	}
	if math.Abs(candidate.FusionScore-wantScore) > 1e-12 {
		t.Fatalf("post 2 fusion score=%v want=%v", candidate.FusionScore, wantScore)
	}
}

func TestRecommendationRRFFusionUsesOneBasedRanks(t *testing.T) {
	got := fuseRecommendationCandidates(1, 60, recommendationRecallList{
		Source:     recommendationRecallSourceSemantic,
		Candidates: []embeddingCandidate{{PostID: 1}},
	})
	if len(got) != 1 || got[0].SemanticRank != 1 {
		t.Fatalf("fused candidates=%#v, want rank 1", got)
	}
	if want := 1.0 / 61; math.Abs(got[0].FusionScore-want) > 1e-12 {
		t.Fatalf("fusion score=%v want=%v", got[0].FusionScore, want)
	}
}

func TestRecommendationRRFFusionAppliesLimitAfterAggregation(t *testing.T) {
	got := fuseRecommendationCandidates(
		2,
		60,
		recommendationRecallList{
			Source:     recommendationRecallSourceSemantic,
			Candidates: []embeddingCandidate{{PostID: 1}, {PostID: 2}},
		},
		recommendationRecallList{
			Source:     recommendationRecallSourceFollowing,
			Candidates: []embeddingCandidate{{PostID: 3}},
		},
		recommendationRecallList{
			Source:     recommendationRecallSourceRecent,
			Candidates: []embeddingCandidate{{PostID: 3}},
		},
	)
	if len(got) != 2 || got[0].PostID != 3 {
		t.Fatalf("fused candidates=%#v, want post 3 to compete for top 2", got)
	}
	if got[0].SourceCount != 2 || got[0].FollowingRank != 1 || got[0].RecentRank != 1 {
		t.Fatalf("post 3 metadata=%#v", got[0])
	}
}

func TestRecommendationRRFFusionRewardsCrossSourceAgreement(t *testing.T) {
	got := fuseRecommendationCandidates(
		2,
		60,
		recommendationRecallList{
			Source:     recommendationRecallSourceSemantic,
			Candidates: []embeddingCandidate{{PostID: 1}, {PostID: 2}},
		},
		recommendationRecallList{
			Source:     recommendationRecallSourceRecent,
			Candidates: []embeddingCandidate{{PostID: 2}},
		},
	)
	if len(got) != 2 || got[0].PostID != 2 || got[1].PostID != 1 {
		t.Fatalf("fused candidates=%#v, want consensus post 2 first", got)
	}
	if got[0].FusionScore <= got[1].FusionScore {
		t.Fatalf("consensus score=%v single-source score=%v", got[0].FusionScore, got[1].FusionScore)
	}
}

func TestRecommendationRRFFusionDeduplicatesWithinSource(t *testing.T) {
	got := fuseRecommendationCandidates(2, 60, recommendationRecallList{
		Source: recommendationRecallSourceSemantic,
		Candidates: []embeddingCandidate{
			{PostID: 1},
			{PostID: 1},
			{PostID: 2},
		},
	})
	candidate, ok := recommendationFusionCandidateByID(got, 1)
	if !ok {
		t.Fatalf("fused candidates=%#v, missing duplicate candidate", got)
	}
	if candidate.SourceCount != 1 || candidate.SemanticRank != 1 {
		t.Fatalf("duplicate candidate metadata=%#v", candidate)
	}
	if want := 1.0 / 61; math.Abs(candidate.FusionScore-want) > 1e-12 {
		t.Fatalf("duplicate candidate score=%v want one contribution=%v", candidate.FusionScore, want)
	}
}

func TestRecommendationRRFFusionPreservesSemanticSimilarity(t *testing.T) {
	got := fuseRecommendationCandidates(
		2,
		60,
		recommendationRecallList{
			Source:     recommendationRecallSourceFollowing,
			Candidates: []embeddingCandidate{{PostID: 1}},
		},
		recommendationRecallList{
			Source:     recommendationRecallSourceSemantic,
			Candidates: []embeddingCandidate{{PostID: 1, PositiveSemanticSimilarity: .83}},
		},
	)
	if len(got) != 1 || got[0].PositiveSemanticSimilarity != .83 {
		t.Fatalf("fused candidate=%#v, want semantic similarity .83", got)
	}
}

func TestRecommendationRRFFusionUsesDeterministicTieBreak(t *testing.T) {
	comparisonTests := []struct {
		name  string
		left  embeddingCandidate
		right embeddingCandidate
	}{
		{name: "fusion score", left: embeddingCandidate{FusionScore: .2}, right: embeddingCandidate{FusionScore: .1}},
		{name: "source count", left: embeddingCandidate{FusionScore: .2, SourceCount: 2}, right: embeddingCandidate{FusionScore: .2, SourceCount: 1}},
		{name: "best recall rank", left: embeddingCandidate{FusionScore: .2, SourceCount: 2, SemanticRank: 2}, right: embeddingCandidate{FusionScore: .2, SourceCount: 2, SemanticRank: 3}},
		{name: "post id", left: embeddingCandidate{FusionScore: .2, SourceCount: 2, SemanticRank: 2, PostID: 2}, right: embeddingCandidate{FusionScore: .2, SourceCount: 2, SemanticRank: 2, PostID: 1}},
	}
	for _, tc := range comparisonTests {
		t.Run(tc.name, func(t *testing.T) {
			if !recommendationFusionCandidateBefore(tc.left, tc.right) {
				t.Fatalf("left=%#v right=%#v should sort first", tc.left, tc.right)
			}
		})
	}

	lists := []recommendationRecallList{
		{Source: recommendationRecallSourceSemantic, Candidates: []embeddingCandidate{{PostID: 1}, {PostID: 2}}},
		{Source: recommendationRecallSourceFollowing, Candidates: []embeddingCandidate{{PostID: 2}, {PostID: 1}}},
	}
	first := fuseRecommendationCandidates(2, 60, lists...)
	second := fuseRecommendationCandidates(2, 60, lists...)
	if len(first) != 2 || len(second) != 2 || first[0].PostID != 2 || second[0].PostID != 2 || first[1].PostID != 1 || second[1].PostID != 1 {
		t.Fatalf("tie order first=%#v second=%#v, want [2 1]", first, second)
	}
}

func TestRecommendationRRFFusionHandlesEmptyLists(t *testing.T) {
	if got := fuseRecommendationCandidates(10, 60,
		recommendationRecallList{Source: recommendationRecallSourceSemantic},
		recommendationRecallList{Source: recommendationRecallSourceFollowing, Candidates: []embeddingCandidate{}},
	); got != nil {
		t.Fatalf("empty fusion=%#v, want nil", got)
	}
	if got := fuseRecommendationCandidates(10, 60); got != nil {
		t.Fatalf("no-list fusion=%#v, want nil", got)
	}
}

func TestRecommendationRRFFusionFallsBackForInvalidRankConstant(t *testing.T) {
	for _, rankConstant := range []int{0, -1} {
		t.Run("rank_constant", func(t *testing.T) {
			got := fuseRecommendationCandidates(1, rankConstant, recommendationRecallList{
				Source:     recommendationRecallSourceSemantic,
				Candidates: []embeddingCandidate{{PostID: 1}},
			})
			if len(got) != 1 || math.Abs(got[0].FusionScore-1.0/61) > 1e-12 {
				t.Fatalf("rank constant=%d fusion=%#v, want 1/61", rankConstant, got)
			}
		})
	}
}
