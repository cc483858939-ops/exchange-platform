package controllers

import (
	"math"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
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

func TestRecommendationLanguageContextBlendsAndCapsBehaviorEvidence(t *testing.T) {
	cfg := defaultRecommendationConfig()
	browser := recommendationLanguageContext{Browser: recommendationLanguagePrior{ZH: 1}, BrowserPrimary: "zh"}
	behavior := recommendationLanguagePrior{JA: 1}
	context := buildRecommendationLanguageContext(browser, behavior, 5, cfg)
	if context.Source != "blended" {
		t.Fatalf("source=%q want blended", context.Source)
	}
	assertRecommendationLanguageFloat(t, context.BehaviorShare, .5)
	assertRecommendationLanguageFloat(t, context.Combined.ZH, .5)
	assertRecommendationLanguageFloat(t, context.Combined.JA, .5)
	assertRecommendationLanguageFloat(t, context.Combined.EN, 0)

	context = buildRecommendationLanguageContext(browser, behavior, 100, cfg)
	assertRecommendationLanguageFloat(t, context.BehaviorShare, .95)
	assertRecommendationLanguageFloat(t, context.Combined.ZH, .05)
	assertRecommendationLanguageFloat(t, context.Combined.JA, .95)

	behaviorOnly := buildRecommendationLanguageContext(recommendationLanguageContext{}, behavior, 5, cfg)
	if behaviorOnly.Source != "behavior" || behaviorOnly.BehaviorShare != 1 {
		t.Fatalf("behavior-only context=%#v", behaviorOnly)
	}
	browserOnly := buildRecommendationLanguageContext(browser, recommendationLanguagePrior{}, 0, cfg)
	if browserOnly.Source != "browser" || browserOnly.BehaviorShare != 0 {
		t.Fatalf("browser-only context=%#v", browserOnly)
	}
}

func TestRecommendationLanguageContextSanitizesMaterializedProfileValues(t *testing.T) {
	prior, evidence := normalizedMaterializedRecommendationLanguagePrior(math.NaN(), 2, 3, 5)
	assertRecommendationLanguageFloat(t, prior.ZH, 0)
	assertRecommendationLanguageFloat(t, prior.JA, .4)
	assertRecommendationLanguageFloat(t, prior.EN, .6)
	assertRecommendationLanguageFloat(t, evidence, 5)
	prior, evidence = normalizedMaterializedRecommendationLanguagePrior(1, 1, 1, 0)
	if prior != (recommendationLanguagePrior{}) || evidence != 0 {
		t.Fatalf("zero evidence prior=%#v evidence=%v", prior, evidence)
	}
}

func TestRecommendationRankerAppliesOnlyBoundedPositiveLanguageComponent(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	context := recommendationLanguageContext{Combined: recommendationLanguagePrior{ZH: .8, EN: .2}}
	ranked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Post: models.Post{Language: "en"}},
		{Post: models.Post{Language: "zh"}},
		{Post: models.Post{Language: "und"}},
	}, now, cfg, context)
	if ranked[0].Post.Language != "zh" {
		t.Fatalf("ranked languages=%q,%q,%q want zh first", ranked[0].Post.Language, ranked[1].Post.Language, ranked[2].Post.Language)
	}
	assertRecommendationLanguageFloat(t, ranked[0].Breakdown.LanguageAffinity, .8)
	assertRecommendationLanguageFloat(t, ranked[0].Breakdown.LanguageComponent, .28)
	assertRecommendationLanguageFloat(t, ranked[1].Breakdown.LanguageComponent, .07)
	assertRecommendationLanguageFloat(t, ranked[2].Breakdown.LanguageAffinity, 0)

	disabled := cfg
	disabled.LanguageAffinity.Enabled = false
	disabledRanked := rankRecommendationCandidates(userInterestProfile{}, []hydratedRecommendationCandidate{
		{Post: models.Post{Language: "zh"}},
		{Post: models.Post{Language: "en"}},
	}, now, disabled, context)
	if disabledRanked[0].Breakdown.LanguageComponent != 0 || disabledRanked[1].Breakdown.LanguageComponent != 0 {
		t.Fatalf("disabled language components=%v,%v", disabledRanked[0].Breakdown.LanguageComponent, disabledRanked[1].Breakdown.LanguageComponent)
	}
}

func TestRecommendationLanguageBonusDoesNotBeatStrongSemanticSignal(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := rankRecommendationCandidates(userInterestProfile{PositiveVector: []float32{1, 0}}, []hydratedRecommendationCandidate{
		{Post: models.Post{Language: "en"}, Embedding: []float32{1, 0}},
		{Post: models.Post{Language: "zh"}, Embedding: []float32{0, 1}},
	}, now, cfg, recommendationLanguageContext{Combined: recommendationLanguagePrior{ZH: 1, EN: 0}})
	if ranked[0].Post.Language != "en" {
		t.Fatalf("semantic winner language=%q want en", ranked[0].Post.Language)
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
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		persistRecommendationServingTrace = originalPersist
	})

	var received recommendationLanguageContext
	var persisted models.RecommendationRequest
	recommendationServingPathForHandler = func(_ uint, _ uint, cfg config.RecommendationConfig, _ time.Time, _ string, browser recommendationLanguageContext) (recommendationServingOutcome, error) {
		received = browser
		return recommendationServingOutcome{
			Profile:         userInterestProfile{ProfileStatus: recommendationProfileStatusMiss},
			LanguageContext: buildRecommendationLanguageContext(browser, recommendationLanguagePrior{}, 0, cfg),
		}, nil
	}
	selectedRecommendationResponsesForHandler = func([]selectedRecommendation) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}, Score: .5}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	persistRecommendationServingTrace = func(request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
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
