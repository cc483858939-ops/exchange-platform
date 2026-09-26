package recommendation

import (
	"fmt"
	"reflect"
	"testing"

	"Go.exchange/models"
	"gorm.io/gorm"
)

func diversificationTestCandidates(count int) []RankedCandidate {
	result := make([]RankedCandidate, 0, count)
	for index := 1; index <= count; index++ {
		result = append(result, RankedCandidate{
			Candidate: Candidate{PostID: uint(index)},
			Post:      models.Post{Model: gorm.Model{ID: uint(index)}, AuthorID: uint(index)},
			Breakdown: ScoreBreakdown{BaseScore: float64(count - index + 1)},
		})
	}
	return result
}

func diversificationTestIDs(candidates []RankedCandidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Post.ID)
	}
	return ids
}

func TestDiversifyPublicRecommendationCandidatesIsDeterministicForSameRequest(t *testing.T) {
	ranked := diversificationTestCandidates(100)
	first := DiversifyPublicCandidates(ranked, 20, "request-a")
	second := DiversifyPublicCandidates(ranked, 20, "request-a")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same request produced different candidates: first=%v second=%v", diversificationTestIDs(first), diversificationTestIDs(second))
	}
	if len(first) > 40 {
		t.Fatalf("candidate subset length=%d, want <=40", len(first))
	}
}

func TestDiversifyPublicRecommendationCandidatesChangesAcrossRequests(t *testing.T) {
	ranked := diversificationTestCandidates(100)
	first := DiversifyPublicCandidates(ranked, 20, "request-a")
	second := DiversifyPublicCandidates(ranked, 20, "request-b")
	if reflect.DeepEqual(diversificationTestIDs(first), diversificationTestIDs(second)) {
		t.Fatalf("different requests produced identical candidate order: %v", diversificationTestIDs(first))
	}
}

func TestDiversifyPublicRecommendationCandidatesFavorsHighRankedCandidates(t *testing.T) {
	ranked := diversificationTestCandidates(100)
	highRankCount, lowEligibleRankCount := 0, 0
	for seed := 0; seed < 128; seed++ {
		selected := DiversifyPublicCandidates(ranked, 20, fmt.Sprintf("request-%d", seed))
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
	selected := DiversifyPublicCandidates(diversificationTestCandidates(100), 20, "request-window")
	for _, candidate := range selected {
		if candidate.Post.ID < 1 || candidate.Post.ID > 80 {
			t.Fatalf("candidate %d escaped the Top 80 diversification window", candidate.Post.ID)
		}
	}
}

func TestDiversifyPublicRecommendationCandidatesDeduplicatesAndPreservesScores(t *testing.T) {
	ranked := diversificationTestCandidates(100)
	ranked[1].Post.ID = ranked[0].Post.ID
	ranked[1].Breakdown.BaseScore = 987
	selected := DiversifyPublicCandidates(ranked, 20, "request-dedup")
	seen := make(map[uint]struct{}, len(selected))
	for _, candidate := range selected {
		if _, exists := seen[candidate.Post.ID]; exists {
			t.Fatalf("duplicate post id %d in selected=%v", candidate.Post.ID, diversificationTestIDs(selected))
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
	ranked := diversificationTestCandidates(40)
	selected := DiversifyPublicCandidates(ranked, 20, "request-small")
	if !reflect.DeepEqual(selected, ranked) {
		t.Fatalf("small pool changed: got=%v want=%v", diversificationTestIDs(selected), diversificationTestIDs(ranked))
	}
}

func TestDiversifyPublicRecommendationCandidatesHandlesEmptyAndBoundsOutput(t *testing.T) {
	if got := DiversifyPublicCandidates(nil, 20, "request-empty"); len(got) != 0 {
		t.Fatalf("empty input returned %v", got)
	}
	selected := DiversifyPublicCandidates(diversificationTestCandidates(500), 20, "request-bound")
	if len(selected) != 40 {
		t.Fatalf("large pool output length=%d want 40", len(selected))
	}
}
