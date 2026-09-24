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

	ranked := rankRecommendationCandidates(userInterestProfile{PositiveVector: []float32{1, 0}}, []hydratedRecommendationCandidate{candidate}, now, cfg)
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
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{
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
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{
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
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10}},
		{Candidate: embeddingCandidate{PostID: 3, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 3, CreatedAt: now}, AuthorID: 10}},
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
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .8}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}}, Embedding: []float32{.8, .6, 0}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 2, CreatedAt: now}}, Embedding: []float32{.5, 0, .8660254}},
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
			Post:      models.Post{Model: gorm.Model{ID: uint(index + 1), CreatedAt: now}},
			Embedding: embedding,
		}}, now, cfg)
		if len(ranked) != 1 || math.IsNaN(ranked[0].ExplorationSemantic) || math.IsInf(ranked[0].ExplorationSemantic, 0) || ranked[0].ExplorationSemantic < 0 || ranked[0].ExplorationSemantic > 1 {
			t.Fatalf("embedding %v produced unsafe exploration semantic=%v", embedding, ranked[0].ExplorationSemantic)
		}
	}
}

func TestRecommendationRankerComputesSemanticOutsideSemanticRecall(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 1, FromFollowing: true},
		Post:      models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
		Embedding: []float32{1, 0},
	}}, now, cfg)
	if len(ranked) != 1 || math.Abs(ranked[0].Breakdown.PositiveSemantic-1) > 1e-9 || math.Abs(ranked[0].Breakdown.SemanticComponent-cfg.SemanticWeight) > 1e-9 {
		t.Fatalf("ranked=%#v, want source-independent semantic score 1", ranked)
	}
}

func TestRecommendationRankerFallsBackToRecallSemanticWhenHydratedEmbeddingMissing(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .8},
		Post:      models.Post{Model: gorm.Model{ID: 1}, AuthorID: 10},
	}}, now, defaultRecommendationConfig())
	if len(ranked) != 1 || math.Abs(ranked[0].Breakdown.PositiveSemantic-.8) > 1e-9 {
		t.Fatalf("ranked=%#v, want recall semantic fallback .8", ranked)
	}
}

func TestRecommendationRankerKeepsNilEmbeddingWithNonSemanticScores(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	cfg.LanguageAffinity.Enabled = true
	cfg.LanguageAffinity.Weight = 0.25
	profile := userInterestProfile{
		PositiveVector:     []float32{1, 0},
		NegativeVector:     []float32{0, 1},
		NegativeConfidence: 1,
	}
	candidate := hydratedRecommendationCandidate{
		Candidate: embeddingCandidate{
			PostID:       1,
			FromTrending: true,
		},
		Post: models.Post{
			Model:     gorm.Model{ID: 1, CreatedAt: now},
			AuthorID:  10,
			Language:  "en",
			LikeCount: 3,
		},
		// Intentionally no embedding: this is not a semantic recall candidate.
	}
	languageContext := recommendationLanguageContext{
		Combined: recommendationLanguagePrior{EN: 1},
	}

	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{candidate}, now, cfg, languageContext)
	if len(ranked) != 1 {
		t.Fatalf("ranked=%#v, want nil-embedding candidate to remain eligible", ranked)
	}
	got := ranked[0]
	if got.Post.ID != candidate.Post.ID {
		t.Fatalf("ranked post ID=%d want=%d", got.Post.ID, candidate.Post.ID)
	}
	if got.Breakdown.PositiveSemantic != 0 || got.Breakdown.NegativeSemantic != 0 || got.Breakdown.SemanticComponent != 0 {
		t.Fatalf("nil embedding semantic breakdown=%#v, want all semantic scores to be zero", got.Breakdown)
	}
	if got.Breakdown.TrendingComponent <= 0 {
		t.Fatalf("trending component=%v, want positive score", got.Breakdown.TrendingComponent)
	}
	if got.Breakdown.LanguageComponent <= 0 {
		t.Fatalf("language component=%v, want positive score", got.Breakdown.LanguageComponent)
	}
	for name, score := range map[string]float64{
		"base":  got.Breakdown.BaseScore,
		"final": got.Breakdown.FinalScore,
	} {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			t.Errorf("%s score=%v, want finite score", name, score)
		}
	}
	if math.IsNaN(got.ExplorationSemantic) || math.IsInf(got.ExplorationSemantic, 0) || got.ExplorationSemantic < 0 || got.ExplorationSemantic > 1 {
		t.Fatalf("exploration semantic=%v, want finite value within [0,1]", got.ExplorationSemantic)
	}
}

func TestRecommendationRankerKeepsEmbeddedAndNilEmbeddingCandidates(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	ranked := rankRecommendationCandidates(profile, []hydratedRecommendationCandidate{
		{
			Candidate: embeddingCandidate{PostID: 1, FromRecent: true},
			Post:      models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10},
			Embedding: []float32{1, 0},
		},
		{
			Candidate: embeddingCandidate{PostID: 2, FromTrending: true},
			Post:      models.Post{Model: gorm.Model{ID: 2, CreatedAt: now}, AuthorID: 11, LikeCount: 3},
			Embedding: nil,
		},
	}, now, cfg)
	if len(ranked) != 2 {
		t.Fatalf("ranked=%#v, want both candidates retained", ranked)
	}

	byPostID := make(map[uint]hydratedRecommendationCandidate, len(ranked))
	for _, candidate := range ranked {
		byPostID[candidate.Post.ID] = candidate
	}
	embedded, ok := byPostID[1]
	if !ok {
		t.Fatal("embedded post 1 missing from ranked result")
	}
	if math.Abs(embedded.Breakdown.PositiveSemantic-1) > 1e-9 || math.Abs(embedded.Breakdown.SemanticComponent-cfg.SemanticWeight) > 1e-9 {
		t.Fatalf("embedded semantic breakdown=%#v, want similarity 1 and full semantic weight", embedded.Breakdown)
	}
	nonSemantic, ok := byPostID[2]
	if !ok {
		t.Fatal("nil-embedding post 2 missing from ranked result")
	}
	if nonSemantic.Breakdown.PositiveSemantic != 0 || nonSemantic.Breakdown.NegativeSemantic != 0 || nonSemantic.Breakdown.SemanticComponent != 0 {
		t.Fatalf("nil-embedding semantic breakdown=%#v, want all semantic scores to be zero", nonSemantic.Breakdown)
	}
	if nonSemantic.Breakdown.TrendingComponent <= 0 {
		t.Fatalf("nil-embedding trending component=%v, want positive score", nonSemantic.Breakdown.TrendingComponent)
	}
}

func TestRecommendationRankerIgnoresFusionScore(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	candidates := []hydratedRecommendationCandidate{
		{
			Candidate: embeddingCandidate{PostID: 1, FromFollowing: true, FusionScore: .01},
			Post:      models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10},
			Embedding: []float32{1, 0},
		},
		{
			Candidate: embeddingCandidate{PostID: 2, FromFollowing: true, FusionScore: .99},
			Post:      models.Post{Model: gorm.Model{ID: 2, CreatedAt: now}, AuthorID: 10},
			Embedding: []float32{1, 0},
		},
	}
	ranked := rankRecommendationCandidates(profile, candidates, now, defaultRecommendationConfig())
	if len(ranked) != 2 || math.Abs(ranked[0].Breakdown.BaseScore-ranked[1].Breakdown.BaseScore) > 1e-9 {
		t.Fatalf("ranked=%#v, FusionScore must not affect BaseScore", ranked)
	}
}
