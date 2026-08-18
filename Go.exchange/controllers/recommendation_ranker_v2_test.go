package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRecommendationRankerUsesSemanticFreshnessAndPopularityBreakdown(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	article := models.Article{
		Model:       gorm.Model{ID: 1, CreatedAt: now.Add(-48 * time.Hour)},
		AuthorID:    10,
		PublishedAt: ptrTime(now.Add(-48 * time.Hour)),
		LikeCount:   3,
	}
	candidate := hydratedRecommendationCandidate{
		Candidate: embeddingCandidate{ArticleID: 1, FromSemantic: true, SemanticSimilarity: .75},
		Article:   article,
	}

	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{candidate}, now, cfg)
	if len(ranked) != 1 {
		t.Fatalf("ranked=%#v", ranked)
	}
	got := ranked[0].Breakdown
	wantFreshness := cfg.FreshnessWeight * 0.5
	wantPopularity := cfg.PopularityWeight * math.Log1p(3)
	if math.Abs(got.PositiveSemantic-.75) > 1e-9 ||
		math.Abs(got.SemanticComponent-cfg.SemanticWeight*.75) > 1e-9 ||
		math.Abs(got.FreshnessComponent-wantFreshness) > 1e-9 ||
		math.Abs(got.PopularityComponent-wantPopularity) > 1e-9 ||
		got.AuthorAffinityComponent != 0 {
		t.Fatalf("breakdown=%#v", got)
	}
	wantBase := got.SemanticComponent + got.FreshnessComponent + got.PopularityComponent
	if math.Abs(got.BaseScore-wantBase) > 1e-9 {
		t.Fatalf("base score=%v want=%v", got.BaseScore, wantBase)
	}
}

func TestRecommendationRankerUsesDeterministicTieBreak(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	publishedAt := ptrTime(now.Add(-time.Hour))
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1, FromSemantic: true, SemanticSimilarity: .5}, Article: models.Article{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10, PublishedAt: publishedAt}},
		{Candidate: embeddingCandidate{ArticleID: 3, FromSemantic: true, SemanticSimilarity: .5}, Article: models.Article{Model: gorm.Model{ID: 3, CreatedAt: now}, AuthorID: 10, PublishedAt: publishedAt}},
	}, now, cfg)
	if len(ranked) != 2 || ranked[0].Article.ID != 3 || ranked[1].Article.ID != 1 {
		t.Fatalf("ranked=%#v, want IDs [3 1]", ranked)
	}
}
