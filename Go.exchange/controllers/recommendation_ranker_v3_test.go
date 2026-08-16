package controllers

import (
	"math"
	"strings"
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
	canonical := recommendationRankerConfigCanonicalString(cfg)
	if !strings.Contains(canonical, "canonical_outcome=read_end_recency_v2") {
		t.Fatal("canonical outcome version missing from config string")
	}
	baseHash := recommendationRankerConfigHash(cfg)
	variant := cfg
	variant.FeedbackLookbackDays++
	if recommendationRankerConfigHash(variant) == baseHash {
		t.Fatal("feedback lookback did not change V3 config hash")
	}
}

func TestRulesV3StaleReadEndCanBeSupersededByLaterOpen(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		readOutcome string
		openType    string
		wantSignal  string
	}{
		{name: "quick bounce click", readOutcome: recommendationReadOutcomeQuickBounce, openType: "click", wantSignal: "click"},
		{name: "neutral click", readOutcome: recommendationReadOutcomeNeutral, openType: "click", wantSignal: "click"},
		{name: "qualified click", readOutcome: recommendationReadOutcomeQualified, openType: "click", wantSignal: "click"},
		{name: "quick bounce view", readOutcome: recommendationReadOutcomeQuickBounce, openType: "view", wantSignal: "view"},
		{name: "neutral view", readOutcome: recommendationReadOutcomeNeutral, openType: "view", wantSignal: "view"},
		{name: "qualified view", readOutcome: recommendationReadOutcomeQualified, openType: "view", wantSignal: "view"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readAt := now.Add(-10 * time.Minute)
			openAt := now.Add(-time.Minute)
			readOutcome := tc.readOutcome
			state := &recommendationArticleFeedbackState{
				ReadEnd: &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
					ReadOutcome: &readOutcome, OccurredAt: readAt,
				},
			}
			view := models.ArticleBehavior{}
			if tc.openType == "click" {
				state.Click = &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeClick, OccurredAt: openAt,
				}
			} else {
				view = models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: openAt}
			}

			gotSignal, gotAt := resolveRecommendationPassiveOutcome(state, view)
			if gotSignal != tc.wantSignal {
				t.Fatalf("signal=%q want=%q", gotSignal, tc.wantSignal)
			}
			if !gotAt.Equal(openAt) {
				t.Fatalf("occurred_at=%s want=%s", gotAt, openAt)
			}
		})
	}
}

func TestRulesV3LaterReadEndStillTerminatesPassiveOutcome(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		readOutcome string
		wantSignal  string
	}{
		{name: "quick bounce", readOutcome: recommendationReadOutcomeQuickBounce, wantSignal: "quick_bounce"},
		{name: "neutral", readOutcome: recommendationReadOutcomeNeutral, wantSignal: "neutral_read"},
		{name: "qualified", readOutcome: recommendationReadOutcomeQualified, wantSignal: "qualified_read"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clickAt := now.Add(-20 * time.Minute)
			viewAt := now.Add(-15 * time.Minute)
			readAt := now.Add(-10 * time.Minute)
			readOutcome := tc.readOutcome
			state := &recommendationArticleFeedbackState{
				Click: &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeClick, OccurredAt: clickAt,
				},
				ReadEnd: &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
					ReadOutcome: &readOutcome, OccurredAt: readAt,
				},
			}
			view := models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: viewAt}

			gotSignal, gotAt := resolveRecommendationPassiveOutcome(state, view)
			if gotSignal != tc.wantSignal {
				t.Fatalf("signal=%q want=%q", gotSignal, tc.wantSignal)
			}
			if !gotAt.Equal(readAt) {
				t.Fatalf("occurred_at=%s want=%s", gotAt, readAt)
			}
		})
	}
}

func TestRulesV3ClickRemainsPreferredOverLaterDetailView(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		readAt *time.Time
	}{
		{name: "without read end"},
		{name: "after stale read end", readAt: func() *time.Time {
			value := now.Add(-10 * time.Minute)
			return &value
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &recommendationArticleFeedbackState{
				Click: &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeClick,
					OccurredAt: now.Add(-2 * time.Minute),
				},
			}
			if tc.readAt != nil {
				readOutcome := recommendationReadOutcomeNeutral
				state.ReadEnd = &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
					ReadOutcome: &readOutcome, OccurredAt: *tc.readAt,
				}
			}
			viewAt := now.Add(-time.Minute)
			gotSignal, gotAt := resolveRecommendationPassiveOutcome(state, models.ArticleBehavior{
				ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: viewAt,
			})
			if gotSignal != "click" || !gotAt.Equal(now.Add(-2*time.Minute)) {
				t.Fatalf("signal=%q occurred_at=%s", gotSignal, gotAt)
			}
		})
	}
}

func TestRulesV3ReadEndRequiresStrictlyLaterOpenToBeSuperseded(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		openType string
		openAt   time.Time
		want     string
	}{
		{name: "equal click", openType: "click", openAt: now, want: "neutral_read"},
		{name: "equal view", openType: "view", openAt: now, want: "neutral_read"},
		{name: "one nanosecond later click", openType: "click", openAt: now.Add(time.Nanosecond), want: "click"},
		{name: "one nanosecond later view", openType: "view", openAt: now.Add(time.Nanosecond), want: "view"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readOutcome := recommendationReadOutcomeNeutral
			state := &recommendationArticleFeedbackState{
				ReadEnd: &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
					ReadOutcome: &readOutcome, OccurredAt: now,
				},
			}
			view := models.ArticleBehavior{}
			if tc.openType == "click" {
				state.Click = &models.RecommendationEvent{
					ArticleID: 1, EventType: models.RecommendationEventTypeClick, OccurredAt: tc.openAt,
				}
			} else {
				view = models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: tc.openAt}
			}
			gotSignal, gotAt := resolveRecommendationPassiveOutcome(state, view)
			wantAt := now
			if tc.want != "neutral_read" {
				wantAt = tc.openAt
			}
			if gotSignal != tc.want || !gotAt.Equal(wantAt) {
				t.Fatalf("signal=%q occurred_at=%s want=%q at=%s", gotSignal, gotAt, tc.want, wantAt)
			}
		})
	}
}

func TestRulesV3NewerClickRecoversFromStaleQuickBounceAffinity(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(1, now, "backend", []string{"go"}, 0)
	readOutcome := recommendationReadOutcomeQuickBounce
	feedback := []recommendationFeedbackSignal{
		{
			Event: models.RecommendationEvent{
				ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
				ReadOutcome: &readOutcome, OccurredAt: now.Add(-10 * time.Minute),
			},
			SignalType: "quick_bounce", Article: &article,
		},
		{
			Event: models.RecommendationEvent{
				ArticleID: 1, EventType: models.RecommendationEventTypeClick,
				OccurredAt: now.Add(-time.Minute),
			},
			SignalType: "click", Article: &article,
		},
	}
	outcomes := canonicalizeRecommendationOutcomes(nil, feedback, nil)
	if len(outcomes) != 1 || outcomes[0].SignalType != "click" {
		t.Fatalf("canonical outcomes=%#v", outcomes)
	}
	profile := buildRulesV3InterestProfile(nil, feedback, nil, now, normalizedRulesV3RecommendationConfig())
	if profile.PersonalizedSignalCount != 1 || profile.Categories["backend"] <= 0 || profile.Tags["go"] <= 0 {
		t.Fatalf("profile=%#v", profile)
	}
}

func TestRulesV3NewerViewRecoversFromStaleNeutralRead(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(1, now, "backend", []string{"go"}, 0)
	readOutcome := recommendationReadOutcomeNeutral
	feedback := []recommendationFeedbackSignal{{
		Event: models.RecommendationEvent{
			ArticleID: 1, EventType: models.RecommendationEventTypeReadEnd,
			ReadOutcome: &readOutcome, OccurredAt: now.Add(-10 * time.Minute),
		},
		SignalType: "neutral_read", Article: &article,
	}}
	behaviors := []articleBehaviorSignal{{
		Behavior: models.ArticleBehavior{
			ArticleID: 1, Action: ArticleBehaviorActionView, LastSeenAt: now.Add(-time.Minute),
		},
		Article: article,
	}}
	outcomes := canonicalizeRecommendationOutcomes(behaviors, feedback, nil)
	if len(outcomes) != 1 || outcomes[0].SignalType != "view" || !outcomes[0].OccurredAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("canonical outcomes=%#v", outcomes)
	}
	profile := buildRulesV3InterestProfile(behaviors, feedback, nil, now, normalizedRulesV3RecommendationConfig())
	if profile.PersonalizedSignalCount != 1 || profile.Categories["backend"] <= 0 || profile.Tags["go"] <= 0 {
		t.Fatalf("profile=%#v", profile)
	}
}
