package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"
)

func TestRulesV2DecayAndBoundedProfile(t *testing.T) {
	cfg := normalizedRulesV2RecommendationConfig()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if got := rulesV2Decay(now, now.AddDate(0, 0, -14), 14); math.Abs(got-0.5) > 0.00001 {
		t.Fatalf("14-day decay=%f", got)
	}
	if got := rulesV2Decay(now, now.AddDate(0, 0, -28), 14); math.Abs(got-0.25) > 0.00001 {
		t.Fatalf("28-day decay=%f", got)
	}
	if got := rulesV2Decay(now, now.Add(time.Hour), 14); got != 1 {
		t.Fatalf("future decay=%f", got)
	}
	if got := rulesV2Decay(now, time.Time{}, 14); got != 1 {
		t.Fatalf("zero-time decay=%f", got)
	}
	if got := rulesV2Decay(now, now.Add(-time.Hour), 0); got != 1 {
		t.Fatalf("non-positive half-life decay=%f", got)
	}
	article := recommendationTestArticle(1, now, "Go", []string{"backend", "backend"}, 0)
	profile := buildRulesV2InterestProfile([]articleBehaviorSignal{{Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 999, Active: true, LastSeenAt: now}, Article: article}}, nil, now, cfg)
	if profile.PersonalizedSignalCount != 1 || profile.Categories["go"] <= 0 || profile.Categories["go"] > 1 || profile.Tags["backend"] > 1 {
		t.Fatalf("profile=%#v", profile)
	}
}

func TestRulesV2FeedbackSemanticsAndTagAverage(t *testing.T) {
	cfg := normalizedRulesV2RecommendationConfig()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	positive := recommendationTestArticle(1, now, "backend", []string{"go"}, 0)
	negative := recommendationTestArticle(2, now, "travel", []string{"beach"}, 0)
	profile := buildRulesV2InterestProfile(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd, QualifiedRead: true, OccurredAt: now}, SignalType: "qualified_read", Article: &positive},
		{Event: models.RecommendationEvent{ArticleID: 2, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: now}, SignalType: "not_interested", Article: &negative},
		{Event: models.RecommendationEvent{ArticleID: 3, EventType: models.RecommendationEventTypeReadEnd, OccurredAt: now}, SignalType: "neutral_read_end"},
	}, now, cfg)
	if profile.PersonalizedSignalCount != 2 {
		t.Fatalf("count=%d", profile.PersonalizedSignalCount)
	}
	if _, ok := profile.InteractedArticleIDs[3]; !ok {
		t.Fatal("neutral read must exclude article")
	}
	if profile.Categories["backend"] <= 0 || profile.Categories["travel"] >= 0 {
		t.Fatalf("profile=%#v", profile.Categories)
	}
	profile.Tags = map[string]float64{"go": 1}
	candidateOne := recommendationTestArticle(4, now, "", []string{"go"}, 0)
	candidateTwo := recommendationTestArticle(5, now, "", []string{"go", "missing"}, 0)
	one := scoreRulesV2Article(profile, candidateOne, now, cfg)
	two := scoreRulesV2Article(profile, candidateTwo, now, cfg)
	if two >= one {
		t.Fatalf("tag average not applied: one=%f two=%f", one, two)
	}
}

func TestRulesV2StrategyAndWeights(t *testing.T) {
	cfg := normalizedRulesV2RecommendationConfig()
	if rulesV2SignalWeight(cfg, "like") <= rulesV2SignalWeight(cfg, "view") || rulesV2SignalWeight(cfg, "quick_bounce") >= 0 || rulesV2SignalWeight(cfg, "not_interested") >= 0 {
		t.Fatalf("weights=%#v", cfg.BehaviorWeights)
	}
	if recommendationStrategyID(userInterestProfile{}) != recommendationColdStartStrategyID {
		t.Fatal("expected cold start")
	}
	if recommendationStrategyID(userInterestProfile{PersonalizedSignalCount: 1}) != recommendationPersonalizedStrategyID {
		t.Fatal("expected personalized")
	}
}

func TestRulesV2ConfigHashCoversEveryConfigInput(t *testing.T) {
	base := normalizedRulesV2RecommendationConfig()
	baseHash := recommendationRankerConfigHash(base)
	assertChanged := func(name string, variantHash string) {
		t.Helper()
		if variantHash == baseHash {
			t.Fatalf("%s did not change config hash %s", name, baseHash)
		}
	}

	variant := base
	variant.BehaviorWeights.View++
	assertChanged("view", recommendationRankerConfigHash(variant))
	variant = base
	variant.BehaviorWeights.Like++
	assertChanged("like", recommendationRankerConfigHash(variant))
	variant = base
	variant.BehaviorWeights.Click++
	assertChanged("click", recommendationRankerConfigHash(variant))
	variant = base
	variant.BehaviorWeights.QualifiedRead++
	assertChanged("qualified_read", recommendationRankerConfigHash(variant))
	variant = base
	variant.BehaviorWeights.QuickBounce++
	assertChanged("quick_bounce", recommendationRankerConfigHash(variant))
	variant = base
	variant.BehaviorWeights.NotInterested++
	assertChanged("not_interested", recommendationRankerConfigHash(variant))
	variant = base
	variant.SignalHalfLifeDays++
	assertChanged("signal_half_life_days", recommendationRankerConfigHash(variant))
	variant = base
	variant.FeedbackLookbackDays++
	assertChanged("feedback_lookback_days", recommendationRankerConfigHash(variant))
	variant = base
	variant.InterestSaturationScale++
	assertChanged("interest_saturation_scale", recommendationRankerConfigHash(variant))
	variant = base
	variant.CategoryWeight++
	assertChanged("category_weight", recommendationRankerConfigHash(variant))
	variant = base
	variant.TagWeight++
	assertChanged("tag_weight", recommendationRankerConfigHash(variant))
	variant = base
	variant.PopularityWeight++
	assertChanged("popularity_weight", recommendationRankerConfigHash(variant))
	variant = base
	variant.FreshnessWeight++
	assertChanged("freshness_weight", recommendationRankerConfigHash(variant))
}
