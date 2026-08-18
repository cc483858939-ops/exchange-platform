package controllers

import (
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
	cfg.NovelAuthorMinRatio = 0.5
	initial := []selectedRecommendation{{Article: models.Article{Model: gorm.Model{ID: 100}, AuthorID: 10}, Embedding: []float32{0, 1}}}
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1}, Article: models.Article{Model: gorm.Model{ID: 1}, AuthorID: 10}, Embedding: []float32{1, 0}, Breakdown: recommendationScoreBreakdown{BaseScore: 100}, IsNovelAuthor: true},
		{Candidate: embeddingCandidate{ArticleID: 2}, Article: models.Article{Model: gorm.Model{ID: 2}, AuthorID: 20}, Breakdown: recommendationScoreBreakdown{BaseScore: 1}, IsInNetwork: true},
	}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Article.ID != 2 {
		t.Fatalf("selected=%#v, want strict any fallback article 2", selected)
	}
}

func TestRecommendationSelectionUsesStrictOutOfNetworkFallbackBeforeAuthorRelaxation(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 1
	cfg.OutOfNetworkMinRatio = 0.5
	cfg.NovelAuthorMinRatio = 0.5
	initial := []selectedRecommendation{{Article: models.Article{Model: gorm.Model{ID: 100}, AuthorID: 10}}}
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1}, Article: models.Article{Model: gorm.Model{ID: 1}, AuthorID: 10}, Breakdown: recommendationScoreBreakdown{BaseScore: 100}, IsNovelAuthor: true},
		{Candidate: embeddingCandidate{ArticleID: 2}, Article: models.Article{Model: gorm.Model{ID: 2}, AuthorID: 20}, Breakdown: recommendationScoreBreakdown{BaseScore: 1}},
	}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Article.ID != 2 {
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
	cfg.NovelAuthorMinRatio = 0.5
	initial := []selectedRecommendation{{Article: models.Article{Model: gorm.Model{ID: 100}, AuthorID: 10}, Embedding: []float32{1, 0}}}
	candidates := []hydratedRecommendationCandidate{{
		Candidate:     embeddingCandidate{ArticleID: 1},
		Article:       models.Article{Model: gorm.Model{ID: 1}, AuthorID: 10},
		Embedding:     []float32{1, 0},
		Breakdown:     recommendationScoreBreakdown{BaseScore: 100},
		IsNovelAuthor: true,
	}}

	selected := selectRecommendationCandidates(candidates, initial, 2, cfg, now, recommendationSelectionFresh)
	if len(selected) != 2 || selected[1].Article.ID != 1 {
		t.Fatalf("selected=%#v, want relaxed article 1", selected)
	}
	if selected[1].Breakdown.DiversityPenalty != cfg.Diversity.SemanticDuplicatePenalty {
		t.Fatalf("relaxed selection diversity penalty=%v, want %v", selected[1].Breakdown.DiversityPenalty, cfg.Diversity.SemanticDuplicatePenalty)
	}
}
