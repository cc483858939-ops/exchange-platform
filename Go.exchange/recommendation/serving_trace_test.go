package recommendation

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestBuildRecommendationResultTracesPreservesThreeProvenanceStates(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	selected := []SelectedCandidate{
		{
			Post:          models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
			SelectionMode: SelectionModeRanked,
		},
		{
			Post:                   models.Post{Model: gorm.Model{ID: 2}, AuthorID: 20},
			ExplorationOpportunity: true,
			SelectionMode:          SelectionModeRanked,
		},
		{
			Post:                   models.Post{Model: gorm.Model{ID: 3}, AuthorID: 30},
			ExplorationOpportunity: true,
			SelectionMode:          SelectionModeExploration,
			ExplorationReason:      ExplorationReasonRecent,
			ExplorationSemantic:    .5,
		},
	}
	traces := buildResultTraces(models.RecommendationRequest{RequestID: "request-id"}, selected, now, testDefaultRecommendationConfig())
	if len(traces) != 3 {
		t.Fatalf("trace count=%d want=3", len(traces))
	}
	want := []struct {
		opportunity bool
		mode        string
		reason      string
		semantic    float64
	}{
		{false, string(SelectionModeRanked), "", 0},
		{true, string(SelectionModeRanked), "", 0},
		{true, string(SelectionModeExploration), ExplorationReasonRecent, .5},
	}
	for index, trace := range traces {
		if trace.PostID != uint(index+1) || trace.Position != index+1 {
			t.Fatalf("trace[%d]=%#v has wrong identity", index, trace)
		}
		if trace.ExplorationOpportunity != want[index].opportunity || trace.SelectionMode != want[index].mode || trace.ExplorationReason != want[index].reason || trace.ExplorationSemantic != want[index].semantic {
			t.Fatalf("trace[%d] provenance=%#v want opportunity=%t mode=%q reason=%q semantic=%v", index, trace, want[index].opportunity, want[index].mode, want[index].reason, want[index].semantic)
		}
	}
}

func TestBuildResultTracesSanitizesPostLanguageAndAffinity(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	traces := buildResultTraces(models.RecommendationRequest{RequestID: "request-id"}, []SelectedCandidate{{
		Post:      models.Post{Language: "ja"},
		Breakdown: ScoreBreakdown{LanguageAffinity: .6, LanguageComponent: .21},
	}}, now, testDefaultRecommendationConfig())
	if len(traces) != 1 || traces[0].PostLanguage != "ja" {
		t.Fatalf("traces=%#v", traces)
	}
	if math.Abs(traces[0].LanguageAffinity-.6) > 1e-9 || math.Abs(traces[0].LanguageComponent-.21) > 1e-9 {
		t.Fatalf("language trace=%#v", traces[0])
	}

	traces = buildResultTraces(models.RecommendationRequest{RequestID: "request-id"}, []SelectedCandidate{{
		Post:      models.Post{Language: "unsupported"},
		Breakdown: ScoreBreakdown{LanguageAffinity: math.NaN(), LanguageComponent: -1},
	}}, now, testDefaultRecommendationConfig())
	if traces[0].PostLanguage != LanguageUnd || traces[0].LanguageAffinity != 0 || traces[0].LanguageComponent != 0 {
		t.Fatalf("invalid language trace=%#v", traces[0])
	}
}

func TestBuildRecommendationResultTracesPreservesFusionMetadata(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	selected := []SelectedCandidate{{
		Candidate: Candidate{
			PostID: 1, FusionScore: .031, SourceCount: 2,
			SemanticRank: 3, RecentRank: 8,
		},
		Post:          models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
		SelectionMode: SelectionModeRanked,
	}}

	traces := buildResultTraces(models.RecommendationRequest{RequestID: "request-id"}, selected, now, testDefaultRecommendationConfig())
	if len(traces) != 1 {
		t.Fatalf("trace count=%d want=1", len(traces))
	}
	trace := traces[0]
	if trace.FusionScore != .031 || trace.SourceCount != 2 || trace.SemanticRank != 3 || trace.FollowingRank != 0 || trace.RecentRank != 8 || trace.TrendingRank != 0 {
		t.Fatalf("trace fusion metadata=%#v", trace)
	}
}
