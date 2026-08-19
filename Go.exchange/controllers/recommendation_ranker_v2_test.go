package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRecommendationRankerUsesSemanticFreshnessAndTrendingBreakdown(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	article := models.Article{
		Model:       gorm.Model{ID: 1, CreatedAt: now.Add(-48 * time.Hour)},
		AuthorID:    10,
		PublishedAt: ptrTime(now.Add(-48 * time.Hour)),
		LikeCount:   3,
	}
	candidate := hydratedRecommendationCandidate{
		Candidate: embeddingCandidate{ArticleID: 1, FromSemantic: true, PositiveSemanticSimilarity: .75},
		Article:   article,
	}

	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{candidate}, now, cfg)
	if len(ranked) != 1 {
		t.Fatalf("ranked=%#v", ranked)
	}
	got := ranked[0].Breakdown
	wantFreshness := cfg.FreshnessWeight * 0.5
	wantTrending := cfg.TrendingWeight * recommendationTrendingRaw(article, now, cfg)
	if math.Abs(got.PositiveSemantic-.75) > 1e-9 ||
		math.Abs(got.SemanticComponent-cfg.SemanticWeight*.75) > 1e-9 ||
		math.Abs(got.FreshnessComponent-wantFreshness) > 1e-9 ||
		math.Abs(got.TrendingComponent-wantTrending) > 1e-9 ||
		got.AuthorAffinityComponent != 0 {
		t.Fatalf("breakdown=%#v", got)
	}
	wantBase := got.SemanticComponent + got.FreshnessComponent + got.TrendingComponent
	if math.Abs(got.BaseScore-wantBase) > 1e-9 {
		t.Fatalf("base score=%v want=%v", got.BaseScore, wantBase)
	}
}

func TestRecommendationTrendingRawUsesHalfLife(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	base := models.Article{PublishedAt: ptrTime(now), LikeCount: 10, CommentCount: 2}
	halfLife := base
	halfLife.PublishedAt = ptrTime(now.Add(-time.Duration(cfg.Trending.HalfLifeHours * float64(time.Hour))))
	want := recommendationTrendingRaw(base, now, cfg) * 0.5
	if math.Abs(recommendationTrendingRaw(halfLife, now, cfg)-want) > 1e-9 {
		t.Fatalf("half-life raw=%v want=%v", recommendationTrendingRaw(halfLife, now, cfg), want)
	}
}

func TestRecommendationTrendingRawAppliesAgeAndEngagementBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	if got := recommendationTrendingRaw(models.Article{PublishedAt: ptrTime(now), LikeCount: 0, CommentCount: 0}, now, cfg); got != 0 {
		t.Fatalf("zero engagement raw=%v, want 0", got)
	}
	old := models.Article{PublishedAt: ptrTime(now.Add(-time.Duration(cfg.Trending.MaxAgeDays)*24*time.Hour - time.Nanosecond)), LikeCount: 10}
	if got := recommendationTrendingRaw(old, now, cfg); got != 0 {
		t.Fatalf("old raw=%v, want 0", got)
	}
	future := models.Article{PublishedAt: ptrTime(now.Add(time.Hour)), LikeCount: 10}
	if got, want := recommendationTrendingRaw(future, now, cfg), math.Log1p(10); math.Abs(got-want) > 1e-9 {
		t.Fatalf("future raw=%v want=%v", got, want)
	}
}

func TestRecommendationRankerAppliesTrendingIndependentOfRecallSource(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{ArticleID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5},
		Article:   models.Article{PublishedAt: ptrTime(now.Add(-time.Hour)), LikeCount: 10},
	}}, now, cfg)
	if len(ranked) != 1 || ranked[0].Breakdown.TrendingComponent <= 0 {
		t.Fatalf("ranked=%#v, want positive source-independent trending", ranked)
	}
}

func TestRecommendationRankerUsesDeterministicTieBreak(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	publishedAt := ptrTime(now.Add(-time.Hour))
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{ArticleID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Article: models.Article{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10, PublishedAt: publishedAt}},
		{Candidate: embeddingCandidate{ArticleID: 3, FromSemantic: true, PositiveSemanticSimilarity: .5}, Article: models.Article{Model: gorm.Model{ID: 3, CreatedAt: now}, AuthorID: 10, PublishedAt: publishedAt}},
	}, now, cfg)
	if len(ranked) != 2 || ranked[0].Article.ID != 3 || ranked[1].Article.ID != 1 {
		t.Fatalf("ranked=%#v, want IDs [3 1]", ranked)
	}
}
