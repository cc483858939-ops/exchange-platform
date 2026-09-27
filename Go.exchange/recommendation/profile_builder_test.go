package recommendation

import (
	"math"
	"slices"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestBuildEmbeddingInterestProfileUsesCanonicalSignalsAndExcludesMissingVectors(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	readOutcome := "qualified"
	canonical := CanonicalizeOutcomes(
		[]models.PostBehavior{
			{Model: gorm.Model{ID: 1}, PostID: 1, Action: "view", LastSeenAt: now.Add(-time.Hour)},
			{Model: gorm.Model{ID: 3}, PostID: 3, Action: "view", LastSeenAt: now},
		},
		[]FeedbackEvent{{EventID: "2", PostID: 2, EventType: models.RecommendationEventTypeReadEnd, OccurredAt: now, ReadOutcome: &readOutcome}},
		map[uint]ReactionState{3: {Liked: false, StateChangedAt: now}},
	)
	profile, err := BuildInterestProfile(canonical, now, DefaultConfig(), "post_embedding_v1", func([]uint, string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {1, 0}, 2: {0, 1}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(profile.InteractedPostIDs, 3) {
		t.Fatal("missing-vector article must remain excluded")
	}
	if profile.PersonalizedSignalCount != 2 || len(profile.PositiveVector) != 2 {
		t.Fatalf("profile=%#v", profile)
	}
	if math.Abs(float64(profile.PositiveVector[0])) > 0.4 || profile.PositiveVector[1] < 0.8 {
		t.Fatalf("vector=%v", profile.PositiveVector)
	}
}

func TestBuildEmbeddingInterestProfilePassesActiveVersionToLoader(t *testing.T) {
	var gotVersion string

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	canonical := CanonicalizeOutcomes(
		[]models.PostBehavior{{PostID: 1, Action: "view", LastSeenAt: now}}, nil, nil,
	)
	_, err := BuildInterestProfile(canonical, now, DefaultConfig(), "v2", func(_ []uint, version string) (map[uint][]float32, error) {
		gotVersion = version
		return map[uint][]float32{1: {1, 0}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != "v2" {
		t.Fatalf("version=%q want=v2", gotVersion)
	}
}

func TestCanonicalRecommendationOutcomePrecedenceKeepsEqualReadEnd(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	outcome := "qualified"
	outcomes := CanonicalizeOutcomes(
		[]models.PostBehavior{{Model: gorm.Model{ID: 3}, PostID: 9, Action: "view", LastSeenAt: now}},
		[]FeedbackEvent{
			{EventID: "read", PostID: 9, EventType: models.RecommendationEventTypeReadEnd, OccurredAt: now, ReadOutcome: &outcome},
			{EventID: "click", PostID: 9, EventType: models.RecommendationEventTypeClick, OccurredAt: now},
		}, nil,
	).Outcomes
	if len(outcomes) != 1 || outcomes[0].PassiveSignal == nil || outcomes[0].PassiveSignal.SignalType != "qualified_read" || !outcomes[0].PassiveSignal.OccurredAt.Equal(now) || len(outcomes[0].PositiveSignals) != 0 || outcomes[0].NegativeSignal != nil {
		t.Fatalf("outcomes=%#v", outcomes)
	}
}
