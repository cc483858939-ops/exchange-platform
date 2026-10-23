package recommendation

import (
	"reflect"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

type selectionOptimizationGoldenRow struct {
	PostID                 uint
	BaseScore              float64
	DiversityPenalty       float64
	FinalScore             float64
	SelectionMode          SelectionMode
	ExplorationOpportunity bool
	ExplorationReason      ExplorationReason
	ExplorationSemantic    float64
}

func TestSelectCandidatesA23GoldenPriorInitialScoringAndExploration(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	makeSelected := func(id, authorID uint, embedding []float32) SelectedCandidate {
		return SelectedCandidate{
			Candidate: Candidate{PostID: id},
			Post: models.Post{
				Model:    gorm.Model{ID: id, CreatedAt: now.Add(-time.Hour)},
				AuthorID: authorID,
			},
			Embedding: embedding,
		}
	}
	makeCandidate := func(
		id, authorID uint,
		score, explorationSemantic float64,
		embedding []float32,
		novel bool,
	) RankedCandidate {
		return RankedCandidate{
			Candidate: Candidate{PostID: id, FromRecent: true},
			Post: models.Post{
				Model:    gorm.Model{ID: id, CreatedAt: now.Add(-time.Hour)},
				AuthorID: authorID,
			},
			Embedding:           embedding,
			Breakdown:           ScoreBreakdown{BaseScore: score},
			ExplorationSemantic: explorationSemantic,
			IsNovelAuthor:       novel,
		}
	}

	selected := SelectCandidates(SelectionInput{
		Candidates: []RankedCandidate{
			makeCandidate(1, 1, 120, .99, []float32{1, 0}, true),
			makeCandidate(2, 2, 115, .98, []float32{0, 1}, false),
			makeCandidate(3, 3, 90, .80, []float32{.70710677, .70710677}, false),
			makeCandidate(4, 4, 110, .70, []float32{-1, 0}, true),
			makeCandidate(5, 5, 80, .60, []float32{0, 0}, true),
		},
		Initial:   []SelectedCandidate{makeSelected(901, 98, []float32{0, 1})},
		Prior:     []SelectedCandidate{makeSelected(900, 99, []float32{1, 0})},
		Limit:     3,
		Now:       now,
		Phase:     SelectionPhaseFresh,
		RequestID: "a23-golden-request",
		Config: SelectionConfig{
			Diversity: DiversityConfig{
				Enabled: true, AuthorWindowSize: 8, MaxSameAuthorInWindow: 1,
				SemanticDuplicateThreshold: .9, SemanticDuplicatePenalty: 20,
			},
			Exploration: ExplorationConfig{
				Ratio: .67, MaxSlots: 2, RecentWindowDays: 7, NovelPostMaxAgeDays: 30,
			},
		},
	})

	got := make([]selectionOptimizationGoldenRow, len(selected))
	for index, item := range selected {
		got[index] = selectionOptimizationGoldenRow{
			PostID: item.Post.ID, BaseScore: item.Breakdown.BaseScore,
			DiversityPenalty: item.Breakdown.DiversityPenalty, FinalScore: item.Breakdown.FinalScore,
			SelectionMode: item.SelectionMode, ExplorationOpportunity: item.ExplorationOpportunity,
			ExplorationReason: item.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
		}
	}
	want := []selectionOptimizationGoldenRow{
		{PostID: 901},
		{PostID: 3, BaseScore: 90, FinalScore: 90, SelectionMode: SelectionModeExploration,
			ExplorationOpportunity: true, ExplorationReason: ExplorationReasonRecent, ExplorationSemantic: .8},
		{PostID: 4, BaseScore: 110, FinalScore: 110, SelectionMode: SelectionModeRanked},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection golden mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
