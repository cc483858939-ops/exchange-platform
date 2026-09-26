package controllers

import (
	"context"
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestCanonicalV2PreservesLikeReplyAndNIPrecedence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	readOutcome := recommendationReadOutcomeQualified
	outcomes := canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{
			{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(time.Minute)}},
		},
		[]recommendationFeedbackSignal{
			{Event: recommendationFeedbackEvent{PostID: 7, EventID: "read", EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &readOutcome}},
		},
		map[uint]recommendationReactionState{7: {Liked: true, StateChangedAt: now.Add(2 * time.Minute)}},
	)
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 2 {
		t.Fatalf("outcomes=%#v", outcomes)
	}
	if outcomes[0].PositiveSignals[0].SignalType != "like" || outcomes[0].PositiveSignals[1].SignalType != "reply" {
		t.Fatalf("positive signals=%#v", outcomes[0].PositiveSignals)
	}

	ni := recommendationFeedbackSignal{Event: recommendationFeedbackEvent{
		PostID: 7, EventID: "ni", EventType: recommendationFeedbackEventTypeNotInterested, OccurredAt: now.Add(3 * time.Minute),
	}}
	outcomes = canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(2 * time.Minute)}}},
		[]recommendationFeedbackSignal{ni},
		map[uint]recommendationReactionState{7: {Liked: true, StateChangedAt: now.Add(3 * time.Minute)}},
	)
	if len(outcomes) != 1 || outcomes[0].NegativeSignal == nil || outcomes[0].NegativeSignal.SignalType != "not_interested" || len(outcomes[0].PositiveSignals) != 0 {
		t.Fatalf("equal-time NI must win: %#v", outcomes)
	}

	outcomes = canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(4 * time.Minute)}}},
		[]recommendationFeedbackSignal{ni},
		nil,
	)
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 1 || outcomes[0].PositiveSignals[0].SignalType != "reply" || outcomes[0].NegativeSignal != nil {
		t.Fatalf("later reply must restore positive precedence: %#v", outcomes)
	}
}

func TestRecommendationProfileCapsPositivePostAndSeparatesNegativeVector(t *testing.T) {
	original := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {1, 0}, 2: {0, 1}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()
	cfg.PositivePostWeightCap = cfg.BehaviorWeights.Reply
	quick := recommendationReadOutcomeQuickBounce
	profile, err := buildEmbeddingInterestProfile(
		context.Background(),
		[]postBehaviorSignal{
			{Behavior: models.PostBehavior{Model: gorm.Model{ID: 1}, PostID: 1, Action: PostBehaviorActionReply, LastSeenAt: now}},
			{Behavior: models.PostBehavior{Model: gorm.Model{ID: 2}, PostID: 2, Action: PostBehaviorActionView, LastSeenAt: now}},
		},
		[]recommendationFeedbackSignal{
			{Event: recommendationFeedbackEvent{PostID: 2, EventID: "bounce", EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &quick}},
		},
		map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: now}},
		now, cfg, "post_embedding_v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveContributions[1] != cfg.PositivePostWeightCap {
		t.Fatalf("like+reply contribution=%v want cap=%v", profile.PositiveContributions[1], cfg.PositivePostWeightCap)
	}
	if profile.PositiveSignalCount != 1 || profile.NegativeSignalCount != 1 {
		t.Fatalf("profile counts=%#v", profile)
	}
	if len(profile.PositiveVector) == 0 || len(profile.NegativeVector) == 0 || profile.NegativeConfidence <= 0 || profile.NegativeConfidence >= 1 {
		t.Fatalf("profile vectors/confidence=%#v", profile)
	}
	if math.Abs(float64(profile.PositiveVector[0])) < 0.99 || math.Abs(float64(profile.NegativeVector[1])) < 0.99 {
		t.Fatalf("profile vector directions positive=%v negative=%v", profile.PositiveVector, profile.NegativeVector)
	}
}
