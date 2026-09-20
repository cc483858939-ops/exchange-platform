package controllers

import (
	"fmt"
	"reflect"
	"testing"

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
	tailRankCount := 0
	for seed := 0; seed < 128; seed++ {
		selected := diversifyPublicRecommendationCandidates(ranked, 20, fmt.Sprintf("request-%d", seed))
		for _, candidate := range selected {
			if candidate.Post.ID <= 10 {
				highRankCount++
			}
			if candidate.Post.ID >= 91 {
				tailRankCount++
			}
		}
	}
	if highRankCount <= tailRankCount {
		t.Fatalf("high-ranked candidates were not favored: high=%d tail=%d", highRankCount, tailRankCount)
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
