package controllers

import (
	"fmt"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRecommendationSelectionUsesStrictFallbackBeforeAuthorRelaxation(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 1
	cfg.OutOfNetworkMinRatio = 0.5
	initial := []selectedRecommendation{{Post: models.Post{Model: gorm.Model{ID: 100}, AuthorID: 10}, Embedding: []float32{0, 1}}}
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1}, Post: models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10}, Embedding: []float32{1, 0}, Breakdown: recommendationScoreBreakdown{BaseScore: 100}, IsNovelAuthor: true},
		{Candidate: embeddingCandidate{PostID: 2}, Post: models.Post{Model: gorm.Model{ID: 2}, AuthorID: 20}, Breakdown: recommendationScoreBreakdown{BaseScore: 1}, IsInNetwork: true},
	}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Post.ID != 2 {
		t.Fatalf("selected=%#v, want strict any fallback article 2", selected)
	}
}

func TestRecommendationSelectionUsesStrictOutOfNetworkFallbackBeforeAuthorRelaxation(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 1
	cfg.OutOfNetworkMinRatio = 0.5
	initial := []selectedRecommendation{{Post: models.Post{Model: gorm.Model{ID: 100}, AuthorID: 10}}}
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1}, Post: models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10}, Breakdown: recommendationScoreBreakdown{BaseScore: 100}, IsNovelAuthor: true},
		{Candidate: embeddingCandidate{PostID: 2}, Post: models.Post{Model: gorm.Model{ID: 2}, AuthorID: 20}, Breakdown: recommendationScoreBreakdown{BaseScore: 1}},
	}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Post.ID != 2 {
		t.Fatalf("selected=%#v, want strict out-of-network fallback article 2", selected)
	}
}

func TestRecommendationSelectionRelaxesAuthorAfterStrictPoolsExhausted(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 1
	cfg.OutOfNetworkMinRatio = 0.5
	initial := []selectedRecommendation{{Post: models.Post{Model: gorm.Model{ID: 100}, AuthorID: 10}, Embedding: []float32{1, 0}}}
	candidates := []hydratedRecommendationCandidate{{
		Candidate:     embeddingCandidate{PostID: 1},
		Post:          models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
		Embedding:     []float32{1, 0},
		Breakdown:     recommendationScoreBreakdown{BaseScore: 100},
		IsNovelAuthor: true,
	}}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Post.ID != 1 {
		t.Fatalf("selected=%#v, want relaxed article 1", selected)
	}
	if selected[1].Breakdown.DiversityPenalty != cfg.Diversity.SemanticDuplicatePenalty {
		t.Fatalf("relaxed selection diversity penalty=%v, want %v", selected[1].Breakdown.DiversityPenalty, cfg.Diversity.SemanticDuplicatePenalty)
	}
}

func TestExplorationPositionsVaryAcrossFixedRequestIDs(t *testing.T) {
	layouts := make(map[string]struct{})
	for _, requestID := range []string{"request-a", "request-b", "request-c", "request-d"} {
		positions := explorationPositions(requestID, 20, 2)
		if len(positions) != 2 {
			t.Fatalf("requestID=%q positions=%v want two positions", requestID, positions)
		}
		if positions[0] >= positions[1] || positions[0] < 2 || positions[1] > 19 {
			t.Fatalf("requestID=%q positions=%v must be sorted interior positions", requestID, positions)
		}
		layouts[fmt.Sprint(positions)] = struct{}{}
	}
	if len(layouts) <= 1 {
		t.Fatalf("fixed request IDs produced one exploration layout: %v", layouts)
	}
}

func TestRecommendationStrictExplorationRespectsAuthorWindow(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 2
	selected := []selectedRecommendation{
		{Post: models.Post{Model: gorm.Model{ID: 100}, AuthorID: 10}},
		{Post: models.Post{Model: gorm.Model{ID: 101}, AuthorID: 10}},
	}
	authorA := makeExplorationTestCandidate(now, 1, 100, 1, true, false)
	authorA.Post.AuthorID = 10
	authorB := makeExplorationTestCandidate(now, 2, 1, .5, true, false)
	authorB.Post.AuthorID = 20
	_, chosen, ok := chooseStrictExplorationCandidate([]hydratedRecommendationCandidate{authorA, authorB}, selected, func(hydratedRecommendationCandidate) bool { return true }, nil, 1, cfg, now)
	if !ok || chosen.Post.ID != 2 {
		t.Fatalf("strict selection=%#v ok=%v want author B article 2", chosen, ok)
	}
}

func TestRecommendationStrictExplorationDoesNotRelaxAuthorWindow(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .5
	cfg.Exploration.MaxSlots = 2
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 2
	initial := []selectedRecommendation{
		{Post: models.Post{Model: gorm.Model{ID: 100}, AuthorID: 10}, SelectionMode: recommendationResultSelectionRanked},
		{Post: models.Post{Model: gorm.Model{ID: 101}, AuthorID: 10}, SelectionMode: recommendationResultSelectionRanked},
	}
	candidates := []hydratedRecommendationCandidate{
		func() hydratedRecommendationCandidate {
			candidate := makeExplorationTestCandidate(now, 1, 100, 1, true, false)
			candidate.Post.AuthorID = 10
			return candidate
		}(),
		func() hydratedRecommendationCandidate {
			candidate := makeExplorationTestCandidate(now, 2, 90, .9, true, false)
			candidate.Post.AuthorID = 10
			return candidate
		}(),
	}
	if _, _, ok := chooseStrictExplorationCandidate(candidates, initial, func(hydratedRecommendationCandidate) bool { return true }, nil, 3, cfg, now); ok {
		t.Fatal("strict exploration must reject every author-window candidate")
	}
	selected := selectRecommendationCandidates(candidates, initial, 4, cfg, now, recommendationSelectionFresh, "author-window-no-relaxation")
	if len(selected) != 4 {
		t.Fatalf("selected length=%d want=4", len(selected))
	}
	if selected[2].Post.ID != 1 || selected[2].SelectionMode != recommendationResultSelectionRanked || !selected[2].ExplorationOpportunity {
		t.Fatalf("normal fallback at opportunity=%#v", selected[2])
	}
	if selected[3].Post.ID != 2 || selected[3].SelectionMode != recommendationResultSelectionRanked || selected[3].ExplorationOpportunity {
		t.Fatalf("post-opportunity fallback=%#v", selected[3])
	}
}

func TestRecommendationExplorationPrefersOutOfNetworkAtOutPreferredPosition(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = false
	inNetwork := makeExplorationTestCandidate(now, 1, 100, 1, true, false)
	inNetwork.IsInNetwork = true
	outOfNetwork := makeExplorationTestCandidate(now, 2, 1, .5, true, false)
	outOfNetwork.IsInNetwork = false
	_, chosen, ok := chooseStrictExplorationCandidate([]hydratedRecommendationCandidate{inNetwork, outOfNetwork}, nil, func(hydratedRecommendationCandidate) bool { return true }, map[int]struct{}{1: {}}, 1, cfg, now)
	if !ok || chosen.Post.ID != 2 {
		t.Fatalf("strict OON selection=%#v ok=%v want article 2", chosen, ok)
	}
}

func TestRecommendationExplorationFallsBackToAnyStrictCandidate(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = false
	inNetwork := makeExplorationTestCandidate(now, 1, 1, .5, true, false)
	inNetwork.IsInNetwork = true
	_, chosen, ok := chooseStrictExplorationCandidate([]hydratedRecommendationCandidate{inNetwork}, nil, func(hydratedRecommendationCandidate) bool { return true }, map[int]struct{}{1: {}}, 1, cfg, now)
	if !ok || chosen.Post.ID != 1 {
		t.Fatalf("any-strict fallback=%#v ok=%v want in-network article 1", chosen, ok)
	}
}

func TestRecommendationExplorationFallsBackToNormalWithoutUnderfill(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .34
	cfg.Exploration.MaxSlots = 1
	cfg.OutOfNetworkMinRatio = 1
	cfg.Diversity.Enabled = false
	candidates := []hydratedRecommendationCandidate{
		makeExplorationTestCandidate(now, 1, 3, 0, false, false),
		makeExplorationTestCandidate(now, 2, 2, 0, false, false),
		makeExplorationTestCandidate(now, 3, 1, 0, false, false),
	}
	for index := range candidates {
		candidates[index].IsInNetwork = true
	}
	selected := selectRecommendationCandidates(candidates, nil, 3, cfg, now, recommendationSelectionFresh, "normal-fallback")
	if len(selected) != 3 {
		t.Fatalf("selected length=%d want=3", len(selected))
	}
	if !selected[1].ExplorationOpportunity || selected[1].SelectionMode != recommendationResultSelectionRanked || selected[1].ExplorationReason != "" || selected[1].ExplorationSemantic != 0 {
		t.Fatalf("normal fallback provenance=%#v", selected[1])
	}
}
