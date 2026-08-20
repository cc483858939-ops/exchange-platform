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

	softCandidates := append([]hydratedRecommendationCandidate(nil), candidates...)
	for index := range softCandidates {
		softCandidates[index].Candidate.WasSoftServed = true
		softCandidates[index].Candidate.LastServedAt = now.Add(-time.Duration(index+1) * time.Hour)
	}
	soft := selectRecommendationCandidates(softCandidates, nil, 3, cfg, now, recommendationSelectionSoft, "natural-and-displaced")
	if len(soft) == 0 {
		t.Fatal("soft selection must use real soft-served candidates")
	}
	for _, item := range soft {
		if item.ExplorationOpportunity || item.SelectionMode != recommendationResultSelectionRanked {
			t.Fatalf("soft selection must disable exploration: %#v", soft)
		}
		if item.ExplorationReason != "" || item.ExplorationSemantic != 0 {
			t.Fatalf("soft selection provenance=%#v", item)
		}
	}
}

func explorationTimePtr(value time.Time) *time.Time {
	return &value
}

func makeExplorationTestCandidate(now time.Time, id uint, baseScore, explorationSemantic float64, recent, novel bool) hydratedRecommendationCandidate {
	at := now.Add(-time.Hour)
	return hydratedRecommendationCandidate{
		Candidate: embeddingCandidate{ArticleID: id, FromRecent: recent},
		Article: models.Article{
			Model:       gorm.Model{ID: id, CreatedAt: at},
			AuthorID:    id,
			PublishedAt: explorationTimePtr(at),
		},
		IsNovelAuthor:       novel,
		ExplorationSemantic: explorationSemantic,
		Breakdown:           recommendationScoreBreakdown{BaseScore: baseScore},
	}
}

func TestRecommendationSoftSelectionAppendsRealCandidatesWithoutExploration(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	initial := []selectedRecommendation{
		{Article: models.Article{Model: gorm.Model{ID: 100}}, SelectionMode: recommendationResultSelectionRanked},
		{Article: models.Article{Model: gorm.Model{ID: 101}}, SelectionMode: recommendationResultSelectionRanked},
	}
	softCandidates := []hydratedRecommendationCandidate{
		makeExplorationTestCandidate(now, 1, 5, .9, true, false),
		makeExplorationTestCandidate(now, 2, 10, .8, true, false),
	}
	softCandidates[0].Candidate.WasSoftServed = true
	softCandidates[0].Candidate.LastServedAt = now.Add(-2 * time.Hour)
	softCandidates[1].Candidate.WasSoftServed = true
	softCandidates[1].Candidate.LastServedAt = now.Add(-time.Hour)

	selected := selectRecommendationCandidates(softCandidates, initial, 4, cfg, now, recommendationSelectionSoft, "soft-extension")
	if len(selected) != 4 {
		t.Fatalf("selected length=%d want=4", len(selected))
	}
	if selected[0].Article.ID != 100 || selected[1].Article.ID != 101 {
		t.Fatalf("initial fresh results changed: %#v", selected)
	}
	if selected[2].Article.ID != 1 || selected[3].Article.ID != 2 {
		t.Fatalf("soft LastServedAt ordering selected=%#v", selected)
	}
	for index, item := range selected[2:] {
		if item.ExplorationOpportunity || item.SelectionMode != recommendationResultSelectionRanked || item.ExplorationReason != "" || item.ExplorationSemantic != 0 {
			t.Fatalf("soft result %d has exploration provenance: %#v", index+2, item)
		}
	}
}

func TestRecommendationExplorationRecentRequiresRecentRecallProvenance(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	candidate := makeExplorationTestCandidate(now, 1, 1, .5, false, false)
	candidate.Candidate.FromSemantic = true
	candidate.Candidate.FromFollowing = true
	candidate.Candidate.FromTrending = true
	if got := recommendationExplorationReason(candidate, now, cfg); got != "" {
		t.Fatalf("young candidate without recent recall reason=%q want empty", got)
	}
}

func TestRecommendationExplorationYoungNonRecentSourcesAreNotEligible(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	for _, source := range []struct {
		name string
		set  func(*embeddingCandidate)
	}{
		{name: "semantic", set: func(candidate *embeddingCandidate) { candidate.FromSemantic = true }},
		{name: "following", set: func(candidate *embeddingCandidate) { candidate.FromFollowing = true }},
		{name: "trending", set: func(candidate *embeddingCandidate) { candidate.FromTrending = true }},
	} {
		t.Run(source.name, func(t *testing.T) {
			candidate := makeExplorationTestCandidate(now, 1, 1, .5, false, false)
			source.set(&candidate.Candidate)
			if got := recommendationExplorationReason(candidate, now, cfg); got != "" {
				t.Fatalf("source=%s reason=%q want empty", source.name, got)
			}
		})
	}
}

func TestRecommendationExplorationNovelAuthorIsIndependentFromRecentRecall(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	candidate := makeExplorationTestCandidate(now, 1, 1, .5, false, true)
	if got := recommendationExplorationReason(candidate, now, cfg); got != recommendationExplorationReasonNovelAuthor {
		t.Fatalf("novel-author reason=%q want=%q", got, recommendationExplorationReasonNovelAuthor)
	}
	candidate.Article.PublishedAt = explorationTimePtr(now.Add(-30 * 24 * time.Hour))
	if got := recommendationExplorationReason(candidate, now, cfg); got != recommendationExplorationReasonNovelAuthor {
		t.Fatalf("boundary novel-author reason=%q want=%q", got, recommendationExplorationReasonNovelAuthor)
	}
	candidate.Article.PublishedAt = explorationTimePtr(now.Add(-30*24*time.Hour - time.Nanosecond))
	if got := recommendationExplorationReason(candidate, now, cfg); got != "" {
		t.Fatalf("over-age novel-author reason=%q want empty", got)
	}
}

func TestRecommendationStrictExplorationRejectsSemanticDuplicatesWhenPenaltyIsZero(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.Enabled = true
	cfg.Diversity.SemanticDuplicateThreshold = .8
	cfg.Diversity.SemanticDuplicatePenalty = 0
	selected := []selectedRecommendation{{Article: models.Article{Model: gorm.Model{ID: 100}}, Embedding: []float32{1, 0}}}
	duplicate := makeExplorationTestCandidate(now, 1, 100, 1, true, false)
	duplicate.Embedding = []float32{1, 0}
	nonDuplicate := makeExplorationTestCandidate(now, 2, 1, .5, true, false)
	nonDuplicate.Embedding = []float32{0, 1}
	available := func(hydratedRecommendationCandidate) bool { return true }
	_, chosen, ok := chooseStrictExplorationCandidate([]hydratedRecommendationCandidate{duplicate, nonDuplicate}, selected, available, nil, 1, cfg, now)
	if !ok || chosen.Article.ID != 2 {
		t.Fatalf("strict selection=%#v ok=%v want non-duplicate article 2", chosen, ok)
	}
	if !recommendationIsSemanticDuplicate(duplicate, selected, cfg) || recommendationDiversityPenalty(duplicate, selected, cfg) != 0 {
		t.Fatal("duplicate gate and penalty must remain independent")
	}
}

func TestRecommendationDuplicateOnlyExplorationFallsBackToRankedWithoutUnderfill(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .5
	cfg.Exploration.MaxSlots = 1
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = true
	cfg.Diversity.SemanticDuplicateThreshold = .8
	cfg.Diversity.SemanticDuplicatePenalty = 0
	initial := []selectedRecommendation{{Article: models.Article{Model: gorm.Model{ID: 100}}, Embedding: []float32{1, 0}, SelectionMode: recommendationResultSelectionRanked}}
	duplicate := makeExplorationTestCandidate(now, 1, 10, 1, true, false)
	duplicate.Embedding = []float32{1, 0}
	normal := makeExplorationTestCandidate(now, 2, 9, 0, false, false)
	selected := selectRecommendationCandidates([]hydratedRecommendationCandidate{duplicate, normal}, initial, 3, cfg, now, recommendationSelectionFresh, "duplicate-only")
	if len(selected) != 3 {
		t.Fatalf("selected length=%d want=3: %#v", len(selected), selected)
	}
	if !selected[1].ExplorationOpportunity || selected[1].SelectionMode != recommendationResultSelectionRanked || selected[1].ExplorationReason != "" || selected[1].ExplorationSemantic != 0 {
		t.Fatalf("duplicate-only fallback provenance=%#v", selected[1])
	}
	for _, item := range selected {
		if item.SelectionMode == recommendationResultSelectionExploration {
			t.Fatalf("duplicate-only fixture unexpectedly explored: %#v", selected)
		}
	}
}

func TestRecommendationExplorationShortageDoesNotUnderfill(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .2
	cfg.Exploration.MaxSlots = 2
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	const limit = 10
	candidates := make([]hydratedRecommendationCandidate, 0, limit)
	for id := uint(1); id <= limit; id++ {
		candidates = append(candidates, makeExplorationTestCandidate(now, id, float64(limit-id+1), 0, false, false))
	}
	selected := selectRecommendationCandidates(candidates, nil, limit, cfg, now, recommendationSelectionFresh, "shortage")
	if len(selected) != limit {
		t.Fatalf("selected length=%d want=%d", len(selected), limit)
	}
	wantOpportunities := recommendationExplorationTarget(limit, cfg)
	actualOpportunities, actualResults := 0, 0
	for _, item := range selected {
		if item.SelectionMode != recommendationResultSelectionRanked {
			t.Fatalf("shortage result explored unexpectedly: %#v", item)
		}
		if item.ExplorationOpportunity {
			actualOpportunities++
		}
		if item.SelectionMode == recommendationResultSelectionExploration {
			actualResults++
		}
	}
	if actualOpportunities != wantOpportunities || actualResults != 0 {
		t.Fatalf("opportunities=%d results=%d want opportunities=%d results=0", actualOpportunities, actualResults, wantOpportunities)
	}
}

func TestRecommendationExplorationDoesNotCarryForwardNaturalOpportunity(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .5
	cfg.Exploration.MaxSlots = 2
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	const limit = 6
	requestID := "no-carry-forward"
	positions := explorationPositions(requestID, limit, recommendationExplorationTarget(limit, cfg))
	if len(positions) != 2 {
		t.Fatalf("positions=%v want two exploration positions", positions)
	}
	firstPosition, secondPosition := positions[0], positions[1]
	candidates := make([]hydratedRecommendationCandidate, 0, limit)
	for position := 1; position < firstPosition; position++ {
		candidates = append(candidates, makeExplorationTestCandidate(now, uint(100+position), float64(100-position), 0, false, false))
	}
	natural := makeExplorationTestCandidate(now, 1, 50, 1, true, false)
	candidates = append(candidates, natural)
	for position := firstPosition + 1; position < secondPosition; position++ {
		candidates = append(candidates, makeExplorationTestCandidate(now, uint(200+position), float64(40-position), 0, false, false))
	}
	exploration := makeExplorationTestCandidate(now, 2, 1, .8, true, false)
	candidates = append(candidates, exploration)
	for position := secondPosition + 1; position <= limit; position++ {
		candidates = append(candidates, makeExplorationTestCandidate(now, uint(300+position), float64(40-position), 0, false, false))
	}
	selected := selectRecommendationCandidates(candidates, nil, limit, cfg, now, recommendationSelectionFresh, requestID)
	if len(selected) != limit {
		t.Fatalf("selected length=%d want=%d: %#v", len(selected), limit, selected)
	}
	if selected[firstPosition-1].Article.ID != natural.Article.ID || selected[firstPosition-1].SelectionMode != recommendationResultSelectionRanked || !selected[firstPosition-1].ExplorationOpportunity {
		t.Fatalf("natural opportunity=%#v want ranked natural candidate", selected[firstPosition-1])
	}
	if selected[secondPosition-1].Article.ID != exploration.Article.ID || selected[secondPosition-1].SelectionMode != recommendationResultSelectionExploration || !selected[secondPosition-1].ExplorationOpportunity {
		t.Fatalf("displaced opportunity=%#v want exploration candidate", selected[secondPosition-1])
	}
	opportunities, results := 0, 0
	for _, item := range selected {
		if item.ExplorationOpportunity {
			opportunities++
		}
		if item.SelectionMode == recommendationResultSelectionExploration {
			results++
		}
	}
	if opportunities != 2 || results != 1 {
		t.Fatalf("opportunities=%d results=%d want 2 and 1", opportunities, results)
	}
}

func TestRecommendationExplorationCountsOpportunitySeparatelyFromDisplacement(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Exploration.Ratio = .1
	cfg.Exploration.MaxSlots = 2
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	const limit = 20
	candidates := make([]hydratedRecommendationCandidate, 0, limit)
	for id := uint(1); id <= limit; id++ {
		candidates = append(candidates, makeExplorationTestCandidate(now, id, float64(limit-id+1), 0, false, false))
	}
	selected := selectRecommendationCandidates(candidates, nil, limit, cfg, now, recommendationSelectionFresh, "count-separation")
	if len(selected) != limit {
		t.Fatalf("selected length=%d want=%d", len(selected), limit)
	}
	if got := countSelectedClass(selected, func(item selectedRecommendation) bool { return item.ExplorationOpportunity }); got != 2 {
		t.Fatalf("opportunity count=%d want=2", got)
	}
	if got := countSelectedClass(selected, func(item selectedRecommendation) bool {
		return item.SelectionMode == recommendationResultSelectionExploration
	}); got != 0 {
		t.Fatalf("result count=%d want=0", got)
	}
}

func TestRecommendationExplorationCountsSeparateTargetOpportunityAndResult(t *testing.T) {
	selected := []selectedRecommendation{
		{SelectionMode: recommendationResultSelectionRanked},
		{ExplorationOpportunity: true, SelectionMode: recommendationResultSelectionRanked},
		{ExplorationOpportunity: true, SelectionMode: recommendationResultSelectionExploration, ExplorationReason: recommendationExplorationReasonRecent},
		{SelectionMode: recommendationResultSelectionRanked},
	}
	counts := recommendationExplorationCountsForSelection(selected, 2)
	if counts.Target != 2 || counts.Opportunities != 2 || counts.Results != 1 {
		t.Fatalf("counts=%#v want target=2 opportunities=2 results=1", counts)
	}
}

func TestRecommendationNovelAuthorCountIsIndependentFromExplorationResultCount(t *testing.T) {
	selected := []selectedRecommendation{
		{IsNovelAuthor: true, SelectionMode: recommendationResultSelectionRanked},
		{IsNovelAuthor: true, SelectionMode: recommendationResultSelectionRanked},
		{ExplorationOpportunity: true, SelectionMode: recommendationResultSelectionExploration, ExplorationReason: recommendationExplorationReasonNovelAuthor},
	}
	if got := countSelectedClass(selected, func(item selectedRecommendation) bool { return item.IsNovelAuthor }); got != 2 {
		t.Fatalf("novel-author count=%d want=2", got)
	}
	if got := countSelectedClass(selected, func(item selectedRecommendation) bool {
		return item.SelectionMode == recommendationResultSelectionExploration
	}); got != 1 {
		t.Fatalf("exploration result count=%d want=1", got)
	}
}
