package controllers

import (
	"math"
	"testing"
	"time"
)

func TestValidEmbeddingVectorRejectsZeroAndNonFiniteVectors(t *testing.T) {
	tests := []struct {
		name   string
		vector []float32
		valid  bool
	}{
		{name: "nil", vector: nil},
		{name: "empty", vector: []float32{}},
		{name: "single zero", vector: []float32{0}},
		{name: "zero vector", vector: []float32{0, 0}},
		{name: "positive nonzero", vector: []float32{1, 0}, valid: true},
		{name: "negative nonzero", vector: []float32{-1, 0}, valid: true},
		{name: "tiny nonzero", vector: []float32{1e-20, 0}, valid: true},
		{name: "nan", vector: []float32{float32(math.NaN()), 0}},
		{name: "positive infinity", vector: []float32{float32(math.Inf(1)), 0}},
		{name: "negative infinity", vector: []float32{float32(math.Inf(-1)), 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validEmbeddingVector(tc.vector); got != tc.valid {
				t.Fatalf("validEmbeddingVector(%v)=%v, want %v", tc.vector, got, tc.valid)
			}
		})
	}
}

func TestBuildEmbeddingInterestProfileDoesNotCountZeroPositiveVector(t *testing.T) {
	original := loadRecommendationArticleEmbeddings
	loadRecommendationArticleEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationArticleEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	profile, err := buildEmbeddingInterestProfile(
		nil,
		nil,
		map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: now}},
		now,
		defaultRecommendationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.PositiveVector) != 0 {
		t.Fatalf("zero positive vector contributed: %#v", profile)
	}
	if _, ok := profile.InteractedArticleIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileDoesNotCountZeroNegativeVector(t *testing.T) {
	original := loadRecommendationArticleEmbeddings
	loadRecommendationArticleEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationArticleEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	quick := recommendationReadOutcomeQuickBounce
	profile, err := buildEmbeddingInterestProfile(
		nil,
		[]recommendationFeedbackSignal{{Event: recommendationFeedbackEvent{
			ArticleID: 1, EventID: "bounce", EventType: recommendationFeedbackEventTypeReadEnd,
			OccurredAt: now, ReadOutcome: &quick,
		}}},
		nil,
		now,
		defaultRecommendationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.NegativeSignalCount != 0 || profile.PersonalizedSignalCount != 0 ||
		len(profile.NegativeVector) != 0 || profile.NegativeConfidence != 0 {
		t.Fatalf("zero negative vector contributed: %#v", profile)
	}
	if _, ok := profile.InteractedArticleIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
}

func TestBuildEmbeddingInterestProfileCountsOnlyNonZeroEmbeddings(t *testing.T) {
	original := loadRecommendationArticleEmbeddings
	loadRecommendationArticleEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {0, 0}, 2: {1, 0}}, nil
	}
	t.Cleanup(func() { loadRecommendationArticleEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	profile, err := buildEmbeddingInterestProfile(
		nil,
		nil,
		map[uint]recommendationReactionState{
			1: {Liked: true, StateChangedAt: now},
			2: {Liked: true, StateChangedAt: now},
		},
		now,
		defaultRecommendationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveSignalCount != 1 || profile.PersonalizedSignalCount != 1 ||
		len(profile.PositiveVector) != 2 || profile.PositiveVector[0] != 1 || profile.PositiveVector[1] != 0 {
		t.Fatalf("profile counts/vector=%#v", profile)
	}
	if _, ok := profile.InteractedArticleIDs[1]; !ok {
		t.Fatal("zero-vector interaction must remain excluded")
	}
	if _, ok := profile.InteractedArticleIDs[2]; !ok {
		t.Fatal("valid-vector interaction must remain excluded")
	}
}
