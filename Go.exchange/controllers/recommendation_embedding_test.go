package controllers

import (
	"math"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestNormalizedEmbeddingRecommendationConfigDefaults(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = nil
	t.Cleanup(func() { config.AppConfig = original })
	cfg := normalizedEmbeddingRecommendationConfig()
	if cfg.BehaviorWeights != (config.RecommendationBehaviorWeights{View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, QuickBounce: -3, NotInterested: -6}) || cfg.SignalHalfLifeDays != 14 || cfg.FeedbackLookbackDays != 90 || cfg.SemanticWeight != 4 || cfg.FreshnessWeight != 2 || cfg.PopularityWeight != 0.5 || cfg.FreshnessHalfLifeDays != 2 {
		t.Fatalf("config=%#v", cfg)
	}
}

func TestBuildEmbeddingInterestProfileUsesCanonicalSignalsAndExcludesMissingVectors(t *testing.T) {
	original := loadRecommendationArticleEmbeddings
	loadRecommendationArticleEmbeddings = func(ids []uint, version string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {1, 0}, 2: {0, 1}}, nil
	}
	t.Cleanup(func() { loadRecommendationArticleEmbeddings = original })
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	readOutcome := recommendationReadOutcomeQualified
	profile, err := buildEmbeddingInterestProfile(
		[]articleBehaviorSignal{
			{Behavior: models.ArticleBehavior{Model: gorm.Model{ID: 1}, ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: now.Add(-time.Hour)}},
			{Behavior: models.ArticleBehavior{Model: gorm.Model{ID: 3}, ArticleID: 3, Action: ArticleBehaviorActionView, LastSeenAt: now}},
		},
		[]recommendationFeedbackSignal{{Event: recommendationFeedbackEvent{EventID: "2", ArticleID: 2, EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &readOutcome}}},
		map[uint]recommendationReactionState{3: {Liked: false, StateChangedAt: now}}, now, normalizedEmbeddingRecommendationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profile.InteractedArticleIDs[3]; !ok {
		t.Fatal("missing-vector article must remain excluded")
	}
	if profile.PersonalizedSignalCount != 2 || len(profile.Vector) != 2 {
		t.Fatalf("profile=%#v", profile)
	}
	if math.Abs(float64(profile.Vector[0])) > 0.4 || profile.Vector[1] < 0.8 {
		t.Fatalf("vector=%v", profile.Vector)
	}
}

func TestBuildEmbeddingInterestProfilePassesActiveVersionToLoader(t *testing.T) {
	originalConfig := config.AppConfig
	originalLoader := loadRecommendationArticleEmbeddings
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "v2"}}
	var gotVersion string
	loadRecommendationArticleEmbeddings = func(_ []uint, version string) (map[uint][]float32, error) {
		gotVersion = version
		return map[uint][]float32{1: {1, 0}}, nil
	}
	t.Cleanup(func() {
		config.AppConfig = originalConfig
		loadRecommendationArticleEmbeddings = originalLoader
	})

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	_, err := buildEmbeddingInterestProfile(
		[]articleBehaviorSignal{{Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: now}}},
		nil,
		nil,
		now,
		normalizedEmbeddingRecommendationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != "v2" {
		t.Fatalf("version=%q want=v2", gotVersion)
	}
}

func TestCanonicalRecommendationOutcomePrecedenceKeepsEqualReadEnd(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	outcome := recommendationReadOutcomeQualified
	outcomes := canonicalizeRecommendationOutcomes(
		[]articleBehaviorSignal{{Behavior: models.ArticleBehavior{Model: gorm.Model{ID: 3}, ArticleID: 9, Action: ArticleBehaviorActionView, LastSeenAt: now}}},
		[]recommendationFeedbackSignal{
			{Event: recommendationFeedbackEvent{EventID: "read", ArticleID: 9, EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &outcome}},
			{Event: recommendationFeedbackEvent{EventID: "click", ArticleID: 9, EventType: recommendationFeedbackEventTypeClick, OccurredAt: now}},
		}, nil,
	)
	if len(outcomes) != 1 || outcomes[0].SignalType != "qualified_read" {
		t.Fatalf("outcomes=%#v", outcomes)
	}
}

func TestMergeEmbeddingCandidatesPreservesSourceFlagsAndCap(t *testing.T) {
	merged := mergeEmbeddingCandidates(4,
		[]embeddingCandidate{{ArticleID: 1, SemanticSimilarity: .9, FromSemantic: true}, {ArticleID: 2, FromSemantic: true}},
		[]embeddingCandidate{{ArticleID: 1, FromRecent: true}, {ArticleID: 3, FromRecent: true}},
		[]embeddingCandidate{{ArticleID: 2, FromPopular: true}},
	)
	if len(merged) != 3 || merged[0].ArticleID != 1 || !merged[0].FromSemantic || !merged[0].FromRecent || merged[1].ArticleID != 2 || !merged[1].FromPopular {
		t.Fatalf("merged=%#v", merged)
	}
}

func TestScoreEmbeddingCandidateUsesSemanticFreshnessAndPopularity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	cfg := config.RecommendationConfig{SemanticWeight: 4, FreshnessWeight: 2, PopularityWeight: .5, FreshnessHalfLifeDays: 2}
	article := models.Article{Model: gorm.Model{ID: 1, CreatedAt: now.Add(-48 * time.Hour)}, LikeCount: 3}
	got := scoreEmbeddingCandidate(embeddingCandidate{FromSemantic: true, SemanticSimilarity: .75}, article, now, cfg)
	want := 4*.75 + 2*.5 + .5*math.Log1p(3)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("score=%f want=%f", got, want)
	}
}

func TestRankEmbeddingCandidatesIsDeterministicAndExcludesInteracted(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	cfg := normalizedEmbeddingRecommendationConfig()
	profile := userInterestProfile{Vector: []float32{1, 0}, InteractedArticleIDs: map[uint]struct{}{2: {}}}
	articles := []models.Article{
		{Model: gorm.Model{ID: 1, CreatedAt: now}, Title: "newer"},
		{Model: gorm.Model{ID: 2, CreatedAt: now.Add(time.Hour)}, Title: "excluded"},
		{Model: gorm.Model{ID: 3, CreatedAt: now}, Title: "tie"},
	}
	candidates := []embeddingCandidate{{ArticleID: 1, FromSemantic: true, SemanticSimilarity: .5}, {ArticleID: 2, FromSemantic: true, SemanticSimilarity: 1}, {ArticleID: 3, FromSemantic: true, SemanticSimilarity: .5}}
	got := rankEmbeddingCandidates(profile, candidates, articles, now, cfg, 10)
	if !reflect.DeepEqual([]uint{got[0].ID, got[1].ID}, []uint{3, 1}) {
		t.Fatalf("ranked=%#v", got)
	}
}
