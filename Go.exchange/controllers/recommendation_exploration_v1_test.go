package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRecommendationExplorationTargetUsesRatioCapAndDisablement(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		ratio float64
		slots int
		want  int
	}{
		{name: "disabled", limit: 20, ratio: 0, slots: 3, want: 0},
		{name: "rounded", limit: 5, ratio: .10, slots: 3, want: 1},
		{name: "slot cap", limit: 50, ratio: .10, slots: 3, want: 3},
		{name: "zero slot cap", limit: 50, ratio: .10, slots: 0, want: 0},
		{name: "limit cap", limit: 2, ratio: .25, slots: 10, want: 1},
		{name: "empty limit", limit: 0, ratio: .10, slots: 3, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultRecommendationConfig()
			cfg.Exploration.Ratio = tc.ratio
			cfg.Exploration.MaxSlots = tc.slots
			if got := recommendationExplorationTarget(tc.limit, cfg); got != tc.want {
				t.Fatalf("target=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestExplorationPositionsAreDeterministicInteriorBiasedAndBounded(t *testing.T) {
	first := explorationPositions("request-a", 20, 6)
	second := explorationPositions("request-a", 20, 6)
	other := explorationPositions("request-b", 20, 6)
	if len(first) != 6 || len(second) != 6 || len(other) != 6 {
		t.Fatalf("positions first=%v second=%v other=%v", first, second, other)
	}
	for index, position := range first {
		if position != second[index] || position < 2 || position > 19 {
			t.Fatalf("positions first=%v second=%v", first, second)
		}
		if index > 0 && first[index-1] >= position {
			t.Fatalf("positions are not unique and sorted: %v", first)
		}
	}
	if len(first) == len(other) {
		same := true
		for index := range first {
			if first[index] != other[index] {
				same = false
				break
			}
		}
		if same {
			t.Fatalf("different request IDs produced the same position set: %v", first)
		}
	}
	if got := explorationPositions("request-a", 3, 3); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("all-slot positions=%v", got)
	}
}

func TestRecommendationExplorationReasonsUseRecentAndNovelAgeCutoffs(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.RecentWindowDays = 7
	cfg.Exploration.NovelArticleMaxAgeDays = 30
	makeCandidate := func(id uint, ageDays float64, recent, novel bool) hydratedRecommendationCandidate {
		at := now.Add(-time.Duration(ageDays * float64(24*time.Hour)))
		return hydratedRecommendationCandidate{
			Candidate:     embeddingCandidate{ArticleID: id, FromRecent: recent},
			Article:       models.Article{Model: gorm.Model{ID: id, CreatedAt: at}, PublishedAt: explorationTimePtr(at)},
			IsNovelAuthor: novel,
		}
	}
	cases := []struct {
		name string
		item hydratedRecommendationCandidate
		want string
	}{
		{name: "recent", item: makeCandidate(1, 7, true, false), want: recommendationExplorationReasonRecent},
		{name: "novel", item: makeCandidate(2, 30, false, true), want: recommendationExplorationReasonNovelAuthor},
		{name: "combined priority", item: makeCandidate(3, 5, true, true), want: recommendationExplorationReasonRecentNovelAuthor},
		{name: "recent cutoff", item: makeCandidate(4, 7.01, true, false), want: ""},
		{name: "novel cutoff", item: makeCandidate(5, 30.01, false, true), want: ""},
		{name: "future clamps to recent", item: makeCandidate(6, -1, true, false), want: recommendationExplorationReasonRecent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recommendationExplorationReason(tc.item, now, cfg); got != tc.want {
				t.Fatalf("reason=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestRecommendationExplorationSemanticUsesMaterializedVectorsAndSafeInputs(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.NegativeSemanticWeight = 1.5
	profile := userInterestProfile{PositiveVector: []float32{1, 0}, NegativeVector: []float32{1, 0}, NegativeConfidence: 1}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1}, Article: models.Article{Model: gorm.Model{ID: 1, CreatedAt: now}}, Embedding: []float32{1, 0}},
		{Candidate: embeddingCandidate{ArticleID: 2}, Article: models.Article{Model: gorm.Model{ID: 2, CreatedAt: now}}, Embedding: []float32{0, 1}},
		{Candidate: embeddingCandidate{ArticleID: 3}, Article: models.Article{Model: gorm.Model{ID: 3, CreatedAt: now}}, Embedding: []float32{1}},
	}, now, cfg)
	byID := make(map[uint]float64, len(ranked))
	for _, item := range ranked {
		byID[item.Article.ID] = item.ExplorationSemantic
	}
	if byID[1] != 0 || byID[2] != 0 || byID[3] != 0 {
		t.Fatalf("exploration semantic=%v, negative evidence or invalid dimensions must be safe", byID)
	}
	profile.NegativeVector = []float32{0, 1}
	ranked = rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{ArticleID: 4}, Article: models.Article{Model: gorm.Model{ID: 4, CreatedAt: now}}, Embedding: []float32{1, 0},
	}}, now, cfg)
	if len(ranked) != 1 || math.Abs(ranked[0].ExplorationSemantic-1) > 1e-9 {
		t.Fatalf("exploration semantic=%v want 1", ranked[0].ExplorationSemantic)
	}
}

func TestRecommendationSemanticDuplicatePredicateIsIndependentOfPenalty(t *testing.T) {
	cfg := defaultRecommendationConfig()
	cfg.Diversity.SemanticDuplicatePenalty = 0
	selected := []selectedRecommendation{{Embedding: []float32{1, 0}}}
	candidate := hydratedRecommendationCandidate{Embedding: []float32{1, 0}}
	if !recommendationIsSemanticDuplicate(candidate, selected, cfg) {
		t.Fatal("duplicate predicate must remain true when penalty is zero")
	}
	if got := recommendationDiversityPenalty(candidate, selected, cfg); got != 0 {
		t.Fatalf("penalty=%v want zero", got)
	}
	candidate.Embedding = []float32{1}
	if recommendationIsSemanticDuplicate(candidate, selected, cfg) {
		t.Fatal("dimension-mismatched embedding must not be a duplicate")
	}
	cfg.Diversity.SemanticDuplicateThreshold = -1
	if recommendationIsSemanticDuplicate(candidate, selected, cfg) {
		t.Fatal("no comparable embeddings must not be a duplicate at the threshold lower bound")
	}
}

func TestRecommendationSelectionRecordsNaturalAndDisplacedExploration(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .34
	cfg.Exploration.MaxSlots = 1
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1}, Article: models.Article{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 1}, Breakdown: recommendationScoreBreakdown{BaseScore: 10}},
		{Candidate: embeddingCandidate{ArticleID: 2, FromRecent: true}, Article: models.Article{Model: gorm.Model{ID: 2, CreatedAt: now}, AuthorID: 2, PublishedAt: explorationTimePtr(now)}, ExplorationSemantic: .8, Breakdown: recommendationScoreBreakdown{BaseScore: 5}},
		{Candidate: embeddingCandidate{ArticleID: 3}, Article: models.Article{Model: gorm.Model{ID: 3, CreatedAt: now}, AuthorID: 3}, Breakdown: recommendationScoreBreakdown{BaseScore: 9}},
	}
	displaced := selectRecommendationCandidates(candidates, nil, 3, cfg, now, recommendationSelectionFresh, "natural-and-displaced")
	if len(displaced) != 3 || displaced[0].Article.ID != 1 || displaced[1].Article.ID != 2 {
		t.Fatalf("displaced selection=%#v", displaced)
	}
	if !displaced[1].ExplorationOpportunity || displaced[1].SelectionMode != recommendationResultSelectionExploration || displaced[1].ExplorationReason != recommendationExplorationReasonRecent || displaced[1].ExplorationSemantic != .8 {
		t.Fatalf("displaced provenance=%#v", displaced[1])
	}

	candidates[1].Breakdown.BaseScore = 11
	natural := selectRecommendationCandidates(candidates, nil, 3, cfg, now, recommendationSelectionFresh, "natural-and-displaced")
	if len(natural) != 3 || !natural[1].ExplorationOpportunity || natural[1].SelectionMode != recommendationResultSelectionRanked || natural[1].ExplorationReason != "" || natural[1].ExplorationSemantic != 0 {
		t.Fatalf("natural provenance=%#v", natural)
	}

	soft := selectRecommendationCandidates(candidates, nil, 3, cfg, now, recommendationSelectionSoft, "natural-and-displaced")
	for _, item := range soft {
		if item.ExplorationOpportunity || item.SelectionMode != recommendationResultSelectionRanked {
			t.Fatalf("soft selection must disable exploration: %#v", soft)
		}
	}
}

func explorationTimePtr(value time.Time) *time.Time {
	return &value
}
