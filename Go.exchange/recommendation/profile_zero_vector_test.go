package recommendation

import (
	"slices"
	"testing"
	"time"

	"Go.exchange/models"
)

func TestBuildEmbeddingInterestProfileDoesNotCountZeroPositiveVector(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	canonical := CanonicalizeOutcomes(nil, nil, map[uint]ReactionState{1: {Liked: true, StateChangedAt: now}})
	profile, err := BuildInterestProfile(canonical, now, DefaultConfig(), "post_embedding_v1", func([]uint, string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.PositiveVector) != 0 {
		t.Fatalf("zero positive vector contributed: %#v", profile)
	}
	if !slices.Contains(profile.InteractedPostIDs, 1) {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileDoesNotCountZeroNegativeVector(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	quick := "quick_bounce"
	canonical := CanonicalizeOutcomes(nil, []FeedbackEvent{{
		PostID: 1, EventID: "bounce", EventType: models.RecommendationEventTypeReadEnd,
		OccurredAt: now, ReadOutcome: &quick,
	}}, nil)
	profile, err := BuildInterestProfile(canonical, now, DefaultConfig(), "post_embedding_v1", func([]uint, string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.NegativeSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.NegativeVector) != 0 || profile.NegativeEvidence != 0 {
		t.Fatalf("zero negative vector contributed: %#v", profile)
	}
	if !slices.Contains(profile.InteractedPostIDs, 1) {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileCountsOnlyNonZeroEmbeddings(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	canonical := CanonicalizeOutcomes(nil, nil, map[uint]ReactionState{
		1: {Liked: true, StateChangedAt: now},
		2: {Liked: true, StateChangedAt: now},
	})
	profile, err := BuildInterestProfile(canonical, now, DefaultConfig(), "post_embedding_v1", func([]uint, string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}, 2: {1, 0}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 1 || profile.PersonalizedSignalCount != 1 ||
		len(profile.PositiveVector) != 2 || profile.PositiveVector[0] != 1 || profile.PositiveVector[1] != 0 {
		t.Fatalf("profile counts/vector=%#v", profile)
	}
	if !slices.Contains(profile.InteractedPostIDs, 1) {
		t.Fatal("zero-vector interaction must remain excluded")
	}
	if !slices.Contains(profile.InteractedPostIDs, 2) {
		t.Fatal("valid-vector interaction must remain excluded")
	}
}
