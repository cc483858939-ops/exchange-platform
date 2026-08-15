package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"
)

func TestRulesV3DecayAndSingleViewContribution(t *testing.T) {
	cfg := normalizedRulesV3RecommendationConfig()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if got := rulesV3Decay(now, now.AddDate(0, 0, -14), 14); math.Abs(got-0.5) > 0.00001 {
		t.Fatalf("14-day decay=%f", got)
	}
	article := recommendationTestArticle(1, now, "Go", []string{"backend", "backend"}, 0)
	behaviors := make([]articleBehaviorSignal, 0, 20)
	for i := 0; i < 20; i++ {
		behaviors = append(behaviors, articleBehaviorSignal{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, Count: int64(i + 1), Active: i%2 == 0, LastSeenAt: now.Add(time.Duration(i) * time.Second)},
			Article:  article,
		})
	}
	profile := buildRulesV3InterestProfile(behaviors, nil, nil, now, cfg)
	if profile.PersonalizedSignalCount != 1 {
		t.Fatalf("repeated views produced %d personalized signals", profile.PersonalizedSignalCount)
	}
	if got := profile.Categories["go"]; got <= 0 || got >= 1 {
		t.Fatalf("unexpected bounded category=%f", got)
	}
}

func TestRulesV3CanonicalPrecedenceAndNeutralInteraction(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(1, now, "backend", []string{"go"}, 0)
	readOutcome := func(value string) *string { return &value }
	feedback := []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeClick, OccurredAt: now.Add(-4 * time.Minute)}, SignalType: "click", Article: &article},
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd, ReadOutcome: readOutcome("qualified"), OccurredAt: now.Add(-3 * time.Minute)}, SignalType: "qualified_read", Article: &article},
	}
	reactions := map[uint]recommendationReactionState{
		1: {Liked: true, StateChangedAt: now.Add(-time.Minute)},
	}
	outcomes := canonicalizeRecommendationOutcomes(nil, feedback, reactions)
	if len(outcomes) != 1 || outcomes[0].SignalType != "like" {
		t.Fatalf("full funnel outcomes=%#v", outcomes)
	}

	neutral := "neutral"
	neutralOutcomes := canonicalizeRecommendationOutcomes(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 2, EventType: models.RecommendationEventTypeClick, OccurredAt: now.Add(-time.Minute)}, Article: &article},
		{Event: models.RecommendationEvent{ArticleID: 2, EventType: models.RecommendationEventTypeReadEnd, ReadOutcome: &neutral, OccurredAt: now}, Article: &article},
	}, nil)
	if len(neutralOutcomes) != 1 || neutralOutcomes[0].SignalType != "neutral_read" {
		t.Fatalf("neutral outcome=%#v", neutralOutcomes)
	}
	profile := buildRulesV3InterestProfile(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 2, EventType: models.RecommendationEventTypeReadEnd, ReadOutcome: &neutral, OccurredAt: now}, Article: &article},
	}, nil, now, normalizedRulesV3RecommendationConfig())
	if profile.PersonalizedSignalCount != 0 {
		t.Fatalf("neutral read should have zero affinity: %#v", profile)
	}
	if _, ok := profile.InteractedArticleIDs[2]; !ok {
		t.Fatal("neutral read should still exclude article")
	}
}

func TestRulesV3CanonicalExplicitReactionOrdering(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(1, now, "backend", []string{"go"}, 0)
	niAt := now.Add(-3 * time.Hour)
	likeAt := now.Add(-2 * time.Hour)
	unlikeAt := now.Add(-time.Hour)
	ni := "not_interested"

	outcome := canonicalizeRecommendationOutcomes(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: niAt, ReadOutcome: nil}, Article: &article},
	}, map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: likeAt}})
	if len(outcome) != 1 || outcome[0].SignalType != "like" {
		t.Fatalf("later like should supersede NI: %#v", outcome)
	}

	outcome = canonicalizeRecommendationOutcomes(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: likeAt}, Article: &article},
	}, map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: niAt}})
	if len(outcome) != 1 || outcome[0].SignalType != "not_interested" {
		t.Fatalf("newer NI should suppress: %#v", outcome)
	}

	outcome = canonicalizeRecommendationOutcomes(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: niAt}, Article: &article},
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeClick, OccurredAt: unlikeAt}, Article: &article},
	}, map[uint]recommendationReactionState{1: {Liked: false, StateChangedAt: unlikeAt}})
	if len(outcome) != 1 || outcome[0].SignalType != "click" {
		t.Fatalf("unlike should not resurrect stale NI but passive click may apply: %#v", outcome)
	}

	outcome = canonicalizeRecommendationOutcomes(nil, []recommendationFeedbackSignal{
		{Event: models.RecommendationEvent{ArticleID: 1, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: now}, Article: &article},
	}, map[uint]recommendationReactionState{1: {Liked: false, StateChangedAt: now.Add(-time.Hour)}})
	if len(outcome) != 1 || outcome[0].SignalType != "not_interested" {
		t.Fatalf("unlike before NI should not suppress NI: %#v", outcome)
	}
	_ = ni
}

func TestRulesV3DistinctLikedArticlesCountSeparately(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	feedback := make([]recommendationFeedbackSignal, 0, 5)
	reactions := make(map[uint]recommendationReactionState, 5)
	for id := uint(1); id <= 5; id++ {
		article := recommendationTestArticle(id, now, "backend", []string{"go"}, 0)
		feedback = append(feedback, recommendationFeedbackSignal{
			Event:   models.RecommendationEvent{ArticleID: id, EventType: models.RecommendationEventTypeClick, OccurredAt: now},
			Article: &article,
		})
		reactions[id] = recommendationReactionState{Liked: true, StateChangedAt: now}
	}
	profile := buildRulesV3InterestProfile(nil, feedback, reactions, now, normalizedRulesV3RecommendationConfig())
	if profile.PersonalizedSignalCount != 5 {
		t.Fatalf("distinct liked articles count=%d want=5", profile.PersonalizedSignalCount)
	}
	if profile.Categories["backend"] <= 0 {
		t.Fatalf("expected shared category affinity: %#v", profile.Categories)
	}
}

func TestRulesV3ReactionOnlyArticleCanContributeAffinity(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(7, now, "backend", []string{"go"}, 0)
	profile := buildRulesV3InterestProfile(nil, nil, map[uint]recommendationReactionState{
		7: {Liked: true, StateChangedAt: now, Article: &article},
	}, now, normalizedRulesV3RecommendationConfig())
	if profile.PersonalizedSignalCount != 1 {
		t.Fatalf("reaction-only article signal count=%d want=1", profile.PersonalizedSignalCount)
	}
	if profile.Categories["backend"] <= 0 {
		t.Fatalf("reaction-only article did not contribute category affinity: %#v", profile.Categories)
	}
}

func TestRulesV3WeightsStrategyAndConfigHash(t *testing.T) {
	cfg := normalizedRulesV3RecommendationConfig()
	if rulesV3SignalWeight(cfg, "like") <= rulesV3SignalWeight(cfg, "view") ||
		rulesV3SignalWeight(cfg, "quick_bounce") >= 0 ||
		rulesV3SignalWeight(cfg, "not_interested") >= 0 {
		t.Fatalf("weights=%#v", cfg.BehaviorWeights)
	}
	if recommendationStrategyID(userInterestProfile{}) != recommendationColdStartStrategyID {
		t.Fatal("expected cold start strategy")
	}
	if recommendationStrategyID(userInterestProfile{PersonalizedSignalCount: 1}) != recommendationPersonalizedStrategyID {
		t.Fatal("expected personalized strategy")
	}
	baseHash := recommendationRankerConfigHash(cfg)
	variant := cfg
	variant.FeedbackLookbackDays++
	if recommendationRankerConfigHash(variant) == baseHash {
		t.Fatal("feedback lookback did not change V3 config hash")
	}
}
