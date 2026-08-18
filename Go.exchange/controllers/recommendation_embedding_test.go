package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"gorm.io/gorm"
)

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
		map[uint]recommendationReactionState{3: {Liked: false, StateChangedAt: now}}, now, defaultRecommendationConfig(),
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
		defaultRecommendationConfig(),
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
	if len(outcomes) != 1 || outcomes[0].PassiveSignal == nil || outcomes[0].PassiveSignal.SignalType != "qualified_read" || !outcomes[0].PassiveSignal.OccurredAt.Equal(now) || len(outcomes[0].PositiveSignals) != 0 || outcomes[0].NegativeSignal != nil {
		t.Fatalf("outcomes=%#v", outcomes)
	}
}
