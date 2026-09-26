package controllers

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"Go.exchange/recommendation"
	"gorm.io/gorm"
)

func assertRecommendationLanguageFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("value=%v want=%v", got, want)
	}
}

func TestParseRecommendationAcceptLanguageNormalizesSupportedRanges(t *testing.T) {
	prior, primary := parseRecommendationAcceptLanguageWithPrimary("zh-CN, ja_JP;q=0.5, en-US;q=0.25, fr;q=1")
	assertRecommendationLanguageFloat(t, prior.ZH, 1/1.75)
	assertRecommendationLanguageFloat(t, prior.JA, .5/1.75)
	assertRecommendationLanguageFloat(t, prior.EN, .25/1.75)
	if primary != "zh" {
		t.Fatalf("primary=%q want zh", primary)
	}
}

func TestParseRecommendationAcceptLanguageRejectsUnsupportedAndInvalidRanges(t *testing.T) {
	for _, raw := range []string{
		"fr-FR, *;q=1, und;q=0.9",
		"zh;q=1.1, ja;q=-0.1, en;q=NaN",
		"zh;q=0, ja;q=0, en;q=0",
	} {
		if got := parseRecommendationAcceptLanguage(raw); got != (recommendationLanguagePrior{}) {
			t.Fatalf("raw=%q prior=%#v want zero", raw, got)
		}
	}
	prior := parseRecommendationAcceptLanguage("zh_CN;q=.4, zh-Hans-CN;q=.9, ja-JP;q=.9")
	assertRecommendationLanguageFloat(t, prior.ZH, .5)
	assertRecommendationLanguageFloat(t, prior.JA, .5)
	if _, primary := parseRecommendationAcceptLanguageWithPrimary("zh_CN;q=.4, zh-Hans-CN;q=.9, ja-JP;q=.9"); primary != "zh" {
		t.Fatalf("primary=%q want first equal-q supported language", primary)
	}
}

func TestParseRecommendationAcceptLanguageIsBounded(t *testing.T) {
	if got := parseRecommendationAcceptLanguage(strings.Repeat("fr-FR,", 200) + "zh"); got != (recommendationLanguagePrior{}) {
		t.Fatalf("overlong header unexpectedly found a language: %#v", got)
	}
	if got := parseRecommendationAcceptLanguage(strings.Repeat("fr-FR,", recommendationAcceptLanguageMaxRanges) + ",zh"); got != (recommendationLanguagePrior{}) {
		t.Fatalf("language after range cap unexpectedly found: %#v", got)
	}
}

func TestRecommendationResultTraceStoresPostLanguageBreakdown(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	traces := buildRecommendationResultTraces(models.RecommendationRequest{RequestID: "request-id"}, []selectedRecommendation{{
		Post:      models.Post{Language: "ja"},
		Breakdown: recommendationScoreBreakdown{LanguageAffinity: .6, LanguageComponent: .21},
	}}, now, defaultRecommendationConfig())
	if len(traces) != 1 || traces[0].PostLanguage != "ja" {
		t.Fatalf("traces=%#v", traces)
	}
	assertRecommendationLanguageFloat(t, traces[0].LanguageAffinity, .6)
	assertRecommendationLanguageFloat(t, traces[0].LanguageComponent, .21)

	traces = buildRecommendationResultTraces(models.RecommendationRequest{RequestID: "request-id"}, []selectedRecommendation{{
		Post:      models.Post{Language: "unsupported"},
		Breakdown: recommendationScoreBreakdown{LanguageAffinity: math.NaN(), LanguageComponent: -1},
	}}, now, defaultRecommendationConfig())
	if traces[0].PostLanguage != recommendationLanguageUnd || traces[0].LanguageAffinity != 0 || traces[0].LanguageComponent != 0 {
		t.Fatalf("invalid language trace=%#v", traces[0])
	}
}

func TestNormalizedRecommendationLanguageAffinityConfigHonorsExplicitZeroAndFalse(t *testing.T) {
	original := config.AppConfig
	t.Cleanup(func() { config.AppConfig = original })
	config.AppConfig = &config.Config{
		Recommendation: config.RecommendationConfig{LanguageAffinity: config.RecommendationLanguageAffinityConfig{Weight: 0, EvidenceSaturationScale: 0, MaxBehaviorShare: 0}},
		RecommendationPresence: map[string]bool{
			"language_affinity.enabled":                   true,
			"language_affinity.weight":                    true,
			"language_affinity.evidence_saturation_scale": true,
			"language_affinity.max_behavior_share":        true,
		},
	}
	got := normalizedRecommendationConfig().LanguageAffinity
	if got.Enabled || got.Weight != 0 || got.MaxBehaviorShare != 0 || got.EvidenceSaturationScale != 5 {
		t.Fatalf("normalized explicit settings=%#v", got)
	}
}

func TestGetPostRecommendationsNormalizesBrowserLanguageWithoutPublicContext(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	var received recommendationLanguageContext
	var persisted models.RecommendationRequest
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ uint, cfg config.RecommendationConfig, _ time.Time, _ string, snapshot recommendationServingSnapshot, browser recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		received = browser
		return recommendationServingOutcome{
			EmbeddingVersion: snapshot.EmbeddingVersion,
			Profile:          userInterestProfile{ProfileStatus: recommendationProfileStatusMiss},
			LanguageContext:  recommendation.BuildLanguageContext(browser, recommendation.LanguagePrior{}, 0, recommendationLanguageConfig(cfg)),
		}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}, Score: .5}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(_ context.Context, _ recommendation.TraceRepository, request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		persisted = request
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	ctx.Request.Header.Set("Accept-Language", "ja-JP, en-US;q=0.2, zh;q=0")
	GetPostRecommendations(ctx)
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if received.BrowserPrimary != "ja" {
		t.Fatalf("received browser context=%#v", received)
	}
	assertRecommendationLanguageFloat(t, received.Browser.JA, 5.0/6.0)
	assertRecommendationLanguageFloat(t, received.Browser.EN, 1.0/6.0)
	if persisted.BrowserLanguagePrimary != "ja" || persisted.LanguageContextSource != "browser" || math.Abs(persisted.LanguageAffinityJA-5.0/6.0) > 1e-9 || persisted.LanguageBehaviorEvidence != 0 || persisted.LanguageBehaviorShare != 0 {
		t.Fatalf("persisted request language context=%#v", persisted)
	}
	if strings.Contains(recorder.Body.String(), "language_context_source") || strings.Contains(recorder.Body.String(), "language_behavior") {
		t.Fatalf("public response leaked language context: %s", recorder.Body.String())
	}
}
