package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRecommendationRankerUsesSemanticAndTrendingBreakdown(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	article := models.Post{
		Model:     gorm.Model{ID: 1, CreatedAt: now.Add(-48 * time.Hour)},
		AuthorID:  10,
		LikeCount: 3,
	}
	candidate := hydratedRecommendationCandidate{
		Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .75},
		Post:      article,
	}

	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{candidate}, now, cfg)
	if len(ranked) != 1 {
		t.Fatalf("ranked=%#v", ranked)
	}
	got := ranked[0].Breakdown
	wantTrending := cfg.TrendingWeight * recommendationTrendingRaw(article, now, cfg)
	if math.Abs(got.PositiveSemantic-.75) > 1e-9 ||
		math.Abs(got.SemanticComponent-cfg.SemanticWeight*.75) > 1e-9 ||
		math.Abs(got.TrendingComponent-wantTrending) > 1e-9 ||
		got.AuthorAffinityComponent != 0 {
		t.Fatalf("breakdown=%#v", got)
	}
	wantBase := got.SemanticComponent + got.TrendingComponent
	if math.Abs(got.BaseScore-wantBase) > 1e-9 {
		t.Fatalf("base score=%v want=%v", got.BaseScore, wantBase)
	}
}

func TestRecommendationRankerPublicationAgeDoesNotChangeBaseScore(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 10}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 2, CreatedAt: now.Add(-365 * 24 * time.Hour)}, AuthorID: 11}},
	}, now, cfg)
	if len(ranked) != 2 || math.Abs(ranked[0].Breakdown.BaseScore-ranked[1].Breakdown.BaseScore) > 1e-9 {
		t.Fatalf("ranked=%#v, publication age must not affect base score", ranked)
	}
}

func TestRecommendationRankerOldRelevantPostBeatsWeakNewArticle(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .9}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now.Add(-365 * 24 * time.Hour)}, AuthorID: 10}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .1}, Post: models.Post{Model: gorm.Model{ID: 2, CreatedAt: now.Add(-time.Hour)}, AuthorID: 11}},
	}, now, cfg)
	if len(ranked) != 2 || ranked[0].Post.ID != 1 {
		t.Fatalf("ranked=%#v, old relevant article should beat weak new article", ranked)
	}
}

func TestRecommendationTrendingRawUsesHalfLife(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	base := models.Post{Model: gorm.Model{CreatedAt: now}, LikeCount: 10, ReplyCount: 2}
	halfLife := base
	halfLife.Model.CreatedAt = now.Add(-time.Duration(cfg.Trending.HalfLifeHours * float64(time.Hour)))
	want := recommendationTrendingRaw(base, now, cfg) * 0.5
	if math.Abs(recommendationTrendingRaw(halfLife, now, cfg)-want) > 1e-9 {
		t.Fatalf("half-life raw=%v want=%v", recommendationTrendingRaw(halfLife, now, cfg), want)
	}
}

func TestRecommendationTrendingRawAppliesAgeAndEngagementBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	if got := recommendationTrendingRaw(models.Post{Model: gorm.Model{CreatedAt: now}, LikeCount: 0, ReplyCount: 0}, now, cfg); got != 0 {
		t.Fatalf("zero engagement raw=%v, want 0", got)
	}
	old := models.Post{Model: gorm.Model{CreatedAt: now.Add(-time.Duration(cfg.Trending.MaxAgeDays)*24*time.Hour - time.Nanosecond)}, LikeCount: 10}
	if got := recommendationTrendingRaw(old, now, cfg); got != 0 {
		t.Fatalf("old raw=%v, want 0", got)
	}
	future := models.Post{Model: gorm.Model{CreatedAt: now.Add(time.Hour)}, LikeCount: 10}
	if got, want := recommendationTrendingRaw(future, now, cfg), math.Log1p(10); math.Abs(got-want) > 1e-9 {
		t.Fatalf("future raw=%v want=%v", got, want)
	}
}

func TestRecommendationRankerAppliesTrendingIndependentOfRecallSource(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5},
		Post:      models.Post{Model: gorm.Model{CreatedAt: now.Add(-time.Hour)}, LikeCount: 10},
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
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10}, PostArticle: &models.PostArticle{PublishedAt: publishedAt}},
		{Candidate: embeddingCandidate{PostID: 3, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 3, CreatedAt: now}, AuthorID: 10}, PostArticle: &models.PostArticle{PublishedAt: publishedAt}},
	}, now, cfg)
	if len(ranked) != 2 || ranked[0].Post.ID != 3 || ranked[1].Post.ID != 1 {
		t.Fatalf("ranked=%#v, want IDs [3 1]", ranked)
	}
}

func TestRecommendationExplorationSemanticHonorsNegativePreference(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.NegativeSemanticWeight = 2
	profile := userInterestProfile{
		PositiveVector:     []float32{1, 0, 0},
		NegativeVector:     []float32{0, 1, 0},
		NegativeConfidence: 1,
	}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .8}, Post: models.Post{Model: gorm.Model{ID: 1}}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)}, Embedding: []float32{.8, .6, 0}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 2}}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)}, Embedding: []float32{.5, 0, .8660254}},
	}, now, cfg)
	if len(ranked) != 2 {
		t.Fatalf("ranked=%#v, want two candidates", ranked)
	}
	semanticByID := map[uint]float64{ranked[0].Post.ID: ranked[0].ExplorationSemantic, ranked[1].Post.ID: ranked[1].ExplorationSemantic}
	if semanticByID[1] != 0 || semanticByID[2] < .49 {
		t.Fatalf("ranked=%#v, want high-negative semantic=0 and neutral semantic near .5", ranked)
	}
}

func TestRecommendationExplorationSemanticInvalidEmbeddingsRemainFiniteAndBounded(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}, NegativeVector: []float32{0, 1}, NegativeConfidence: 1}
	for index, embedding := range [][]float32{
		nil,
		{},
		{1},
		{float32(math.NaN()), 1},
		{float32(math.Inf(1)), 0},
	} {
		ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{{
			Candidate: embeddingCandidate{PostID: uint(index + 1), FromSemantic: true, PositiveSemanticSimilarity: .9},
			Post:      models.Post{Model: gorm.Model{ID: uint(index + 1)}}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)},
			Embedding: embedding,
		}}, now, cfg)
		if len(ranked) != 1 || math.IsNaN(ranked[0].ExplorationSemantic) || math.IsInf(ranked[0].ExplorationSemantic, 0) || ranked[0].ExplorationSemantic < 0 || ranked[0].ExplorationSemantic > 1 {
			t.Fatalf("embedding %v produced unsafe exploration semantic=%v", embedding, ranked[0].ExplorationSemantic)
		}
	}
}
