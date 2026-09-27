package recommendation

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestCanonicalV2PreservesLikeReplyAndNIPrecedence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	readOutcome := "qualified"
	outcomes := CanonicalizeOutcomes(
		[]models.PostBehavior{
			{PostID: 7, Action: "reply", LastSeenAt: now.Add(time.Minute)},
		},
		[]FeedbackEvent{
			{PostID: 7, EventID: "read", EventType: models.RecommendationEventTypeReadEnd, OccurredAt: now, ReadOutcome: &readOutcome},
		},
		map[uint]ReactionState{7: {Liked: true, StateChangedAt: now.Add(2 * time.Minute)}},
	).Outcomes
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 2 {
		t.Fatalf("outcomes=%#v", outcomes)
	}
	if outcomes[0].PositiveSignals[0].SignalType != "like" || outcomes[0].PositiveSignals[1].SignalType != "reply" {
		t.Fatalf("positive signals=%#v", outcomes[0].PositiveSignals)
	}

	ni := FeedbackEvent{
		PostID: 7, EventID: "ni", EventType: models.RecommendationEventTypeNotInterested, OccurredAt: now.Add(3 * time.Minute),
	}
	outcomes = CanonicalizeOutcomes(
		[]models.PostBehavior{{PostID: 7, Action: "reply", LastSeenAt: now.Add(2 * time.Minute)}},
		[]FeedbackEvent{ni},
		map[uint]ReactionState{7: {Liked: true, StateChangedAt: now.Add(3 * time.Minute)}},
	).Outcomes
	if len(outcomes) != 1 || outcomes[0].NegativeSignal == nil || outcomes[0].NegativeSignal.SignalType != "not_interested" || len(outcomes[0].PositiveSignals) != 0 {
		t.Fatalf("equal-time NI must win: %#v", outcomes)
	}

	outcomes = CanonicalizeOutcomes(
		[]models.PostBehavior{{PostID: 7, Action: "reply", LastSeenAt: now.Add(4 * time.Minute)}},
		[]FeedbackEvent{ni}, nil,
	).Outcomes
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 1 || outcomes[0].PositiveSignals[0].SignalType != "reply" || outcomes[0].NegativeSignal != nil {
		t.Fatalf("later reply must restore positive precedence: %#v", outcomes)
	}
}

func TestRecommendationProfileCapsPositivePostAndSeparatesNegativeVector(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.PositivePostWeightCap = cfg.BehaviorWeights.Reply
	quick := "quick_bounce"
	canonical := CanonicalizeOutcomes(
		[]models.PostBehavior{
			{Model: gorm.Model{ID: 1}, PostID: 1, Action: "reply", LastSeenAt: now},
			{Model: gorm.Model{ID: 2}, PostID: 2, Action: "view", LastSeenAt: now},
		},
		[]FeedbackEvent{
			{PostID: 2, EventID: "bounce", EventType: models.RecommendationEventTypeReadEnd, OccurredAt: now, ReadOutcome: &quick},
		},
		map[uint]ReactionState{1: {Liked: true, StateChangedAt: now}},
	)
	profile, err := BuildInterestProfile(canonical, now, cfg, "post_embedding_v1", func([]uint, string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {1, 0}, 2: {0, 1}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveContributions[1] != cfg.PositivePostWeightCap {
		t.Fatalf("like+reply contribution=%v want cap=%v", profile.PositiveContributions[1], cfg.PositivePostWeightCap)
	}
	if profile.PositiveSignalCount != 1 || profile.NegativeSignalCount != 1 {
		t.Fatalf("profile counts=%#v", profile)
	}
	negativeConfidence := math.Tanh(profile.NegativeEvidence / cfg.NegativeConfidenceSaturationScale)
	if len(profile.PositiveVector) == 0 || len(profile.NegativeVector) == 0 || negativeConfidence <= 0 || negativeConfidence >= 1 {
		t.Fatalf("profile vectors/confidence=%#v", profile)
	}
	if math.Abs(float64(profile.PositiveVector[0])) < 0.99 || math.Abs(float64(profile.NegativeVector[1])) < 0.99 {
		t.Fatalf("profile vector directions positive=%v negative=%v", profile.PositiveVector, profile.NegativeVector)
	}
}
