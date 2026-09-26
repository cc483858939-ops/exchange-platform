package recommendation

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"
)

func assertDomainLanguageFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("value=%v want=%v", got, want)
	}
}

func TestRecommendationLanguageContextBlendsAndCapsBehaviorEvidence(t *testing.T) {
	cfg := defaultRecommendationConfig()
	browser := LanguageContext{Browser: LanguagePrior{ZH: 1}, BrowserPrimary: "zh"}
	behavior := LanguagePrior{JA: 1}
	context := BuildLanguageContext(browser, behavior, 5, testRankingConfig(cfg).Language)
	if context.Source != "blended" {
		t.Fatalf("source=%q want blended", context.Source)
	}
	assertDomainLanguageFloat(t, context.BehaviorShare, .5)
	assertDomainLanguageFloat(t, context.Combined.ZH, .5)
	assertDomainLanguageFloat(t, context.Combined.JA, .5)
	assertDomainLanguageFloat(t, context.Combined.EN, 0)

	context = BuildLanguageContext(browser, behavior, 100, testRankingConfig(cfg).Language)
	assertDomainLanguageFloat(t, context.BehaviorShare, .95)
	assertDomainLanguageFloat(t, context.Combined.ZH, .05)
	assertDomainLanguageFloat(t, context.Combined.JA, .95)

	behaviorOnly := BuildLanguageContext(LanguageContext{}, behavior, 5, testRankingConfig(cfg).Language)
	if behaviorOnly.Source != "behavior" || behaviorOnly.BehaviorShare != 1 {
		t.Fatalf("behavior-only context=%#v", behaviorOnly)
	}
	browserOnly := BuildLanguageContext(browser, LanguagePrior{}, 0, testRankingConfig(cfg).Language)
	if browserOnly.Source != "browser" || browserOnly.BehaviorShare != 0 {
		t.Fatalf("browser-only context=%#v", browserOnly)
	}
}

func TestRecommendationLanguageContextSanitizesMaterializedProfileValues(t *testing.T) {
	prior, evidence := NormalizeMaterializedLanguagePrior(math.NaN(), 2, 3, 5)
	assertDomainLanguageFloat(t, prior.ZH, 0)
	assertDomainLanguageFloat(t, prior.JA, .4)
	assertDomainLanguageFloat(t, prior.EN, .6)
	assertDomainLanguageFloat(t, evidence, 5)
	prior, evidence = NormalizeMaterializedLanguagePrior(1, 1, 1, 0)
	if prior != (LanguagePrior{}) || evidence != 0 {
		t.Fatalf("zero evidence prior=%#v evidence=%v", prior, evidence)
	}
}

func TestRecommendationRankerAppliesOnlyBoundedPositiveLanguageComponent(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	languageContext := LanguageContext{Combined: LanguagePrior{ZH: .8, EN: .2}}
	ranked := RankCandidates(ProfileFeatures{}, []RankedCandidate{
		{Post: models.Post{Language: "en"}},
		{Post: models.Post{Language: "zh"}},
		{Post: models.Post{Language: "und"}},
	}, now, testRankingConfig(cfg), languageContext)
	if ranked[0].Post.Language != "zh" {
		t.Fatalf("ranked languages=%q,%q,%q want zh first", ranked[0].Post.Language, ranked[1].Post.Language, ranked[2].Post.Language)
	}
	assertDomainLanguageFloat(t, ranked[0].Breakdown.LanguageAffinity, .8)
	assertDomainLanguageFloat(t, ranked[0].Breakdown.LanguageComponent, .28)
	assertDomainLanguageFloat(t, ranked[1].Breakdown.LanguageComponent, .07)
	assertDomainLanguageFloat(t, ranked[2].Breakdown.LanguageAffinity, 0)

	disabled := cfg
	disabled.LanguageAffinity.Enabled = false
	disabledRanked := RankCandidates(ProfileFeatures{}, []RankedCandidate{
		{Post: models.Post{Language: "zh"}},
		{Post: models.Post{Language: "en"}},
	}, now, testRankingConfig(disabled), languageContext)
	if disabledRanked[0].Breakdown.LanguageComponent != 0 || disabledRanked[1].Breakdown.LanguageComponent != 0 {
		t.Fatalf("disabled language components=%v,%v", disabledRanked[0].Breakdown.LanguageComponent, disabledRanked[1].Breakdown.LanguageComponent)
	}
}

func TestRecommendationLanguageBonusDoesNotBeatStrongSemanticSignal(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cfg := defaultRecommendationConfig()
	ranked := RankCandidates(ProfileFeatures{PositiveVector: []float32{1, 0}}, []RankedCandidate{
		{Post: models.Post{Language: "en"}, Embedding: []float32{1, 0}},
		{Post: models.Post{Language: "zh"}, Embedding: []float32{0, 1}},
	}, now, testRankingConfig(cfg), LanguageContext{Combined: LanguagePrior{ZH: 1}})
	if ranked[0].Post.Language != "en" {
		t.Fatalf("semantic winner language=%q want en", ranked[0].Post.Language)
	}
}
