package controllers

import (
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestBuildRecommendationResultTracesPreservesThreeProvenanceStates(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	selected := []selectedRecommendation{
		{
			Post:          models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
			SelectionMode: recommendationResultSelectionRanked,
		},
		{
			Post:                   models.Post{Model: gorm.Model{ID: 2}, AuthorID: 20},
			ExplorationOpportunity: true,
			SelectionMode:          recommendationResultSelectionRanked,
		},
		{
			Post:                   models.Post{Model: gorm.Model{ID: 3}, AuthorID: 30},
			ExplorationOpportunity: true,
			SelectionMode:          recommendationResultSelectionExploration,
			ExplorationReason:      recommendationExplorationReasonRecent,
			ExplorationSemantic:    .5,
		},
	}
	traces := buildRecommendationResultTraces(models.RecommendationRequest{RequestID: "request-id"}, selected, now, defaultRecommendationConfig())
	if len(traces) != 3 {
		t.Fatalf("trace count=%d want=3", len(traces))
	}
	want := []struct {
		opportunity bool
		mode        string
		reason      string
		semantic    float64
	}{
		{false, string(recommendationResultSelectionRanked), "", 0},
		{true, string(recommendationResultSelectionRanked), "", 0},
		{true, string(recommendationResultSelectionExploration), recommendationExplorationReasonRecent, .5},
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

func TestBuildRecommendationResultTracesPreservesFusionMetadata(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	selected := []selectedRecommendation{{
		Candidate: embeddingCandidate{
			PostID: 1, FusionScore: .031, SourceCount: 2,
			SemanticRank: 3, RecentRank: 8,
		},
		Post:          models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
		SelectionMode: recommendationResultSelectionRanked,
	}}

	traces := buildRecommendationResultTraces(models.RecommendationRequest{RequestID: "request-id"}, selected, now, defaultRecommendationConfig())
	if len(traces) != 1 {
		t.Fatalf("trace count=%d want=1", len(traces))
	}
	trace := traces[0]
	if trace.FusionScore != .031 || trace.SourceCount != 2 || trace.SemanticRank != 3 || trace.FollowingRank != 0 || trace.RecentRank != 8 || trace.TrendingRank != 0 {
		t.Fatalf("trace fusion metadata=%#v", trace)
	}
}
