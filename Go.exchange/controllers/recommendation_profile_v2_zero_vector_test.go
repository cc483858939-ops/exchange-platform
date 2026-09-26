package controllers

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestBuildEmbeddingInterestProfileDoesNotCountZeroPositiveVector(t *testing.T) {
	original := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	profile, err := buildEmbeddingInterestProfile(
		context.Background(),
		nil,
		nil,
		map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: now}},
		now,
		defaultRecommendationConfig(),
		"post_embedding_v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.PositiveVector) != 0 {
		t.Fatalf("zero positive vector contributed: %#v", profile)
	}
	if _, ok := profile.InteractedPostIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileDoesNotCountZeroNegativeVector(t *testing.T) {
	original := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	quick := recommendationReadOutcomeQuickBounce
	profile, err := buildEmbeddingInterestProfile(
		context.Background(),
		nil,
		[]recommendationFeedbackSignal{{Event: recommendationFeedbackEvent{
			PostID: 1, EventID: "bounce", EventType: recommendationFeedbackEventTypeReadEnd,
			OccurredAt: now, ReadOutcome: &quick,
		}}},
		nil,
		now,
		defaultRecommendationConfig(),
		"post_embedding_v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.NegativeSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.NegativeVector) != 0 || profile.NegativeConfidence != 0 {
		t.Fatalf("zero negative vector contributed: %#v", profile)
	}
	if _, ok := profile.InteractedPostIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileCountsOnlyNonZeroEmbeddings(t *testing.T) {
	original := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}, 2: {1, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	profile, err := buildEmbeddingInterestProfile(
		context.Background(),
		nil,
		nil,
		map[uint]recommendationReactionState{
			1: {Liked: true, StateChangedAt: now},
			2: {Liked: true, StateChangedAt: now},
		},
		now,
		defaultRecommendationConfig(),
		"post_embedding_v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 1 || profile.PersonalizedSignalCount != 1 ||
		len(profile.PositiveVector) != 2 || profile.PositiveVector[0] != 1 || profile.PositiveVector[1] != 0 {
		t.Fatalf("profile counts/vector=%#v", profile)
	}
	if _, ok := profile.InteractedPostIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
	if _, ok := profile.InteractedPostIDs[2]; !ok {
		t.Fatal("valid-vector interaction must remain excluded")
	}
}
