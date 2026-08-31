package recommendation

import (
	"math"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestBuildInterestProfileGoldenVectorsAndCounts(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	quick := UserPostOutcome{PostID: 2, NegativeSignal: &UserPostSignal{SignalType: "quick_bounce", OccurredAt: now}}
	like := UserPostOutcome{PostID: 1, PositiveSignals: []UserPostSignal{{SignalType: "like", OccurredAt: now}}}
	cfg := config.RecommendationConfig{
		BehaviorWeights:    config.RecommendationBehaviorWeights{Like: 6, QuickBounce: -3},
		SignalHalfLifeDays: 14, PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
		NegativeConfidenceSaturationScale: 12,
	}
	built, err := BuildInterestProfile(CanonicalizationResult{Outcomes: []UserPostOutcome{like, quick}, InteractedPostIDs: []uint{1, 2}}, now, cfg, "embedding-v1", func(_ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {3, 4}, 2: {1, 0}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if built.PositiveSignalCount != 1 || built.NegativeSignalCount != 1 || built.PersonalizedSignalCount != 2 || built.NegativeEvidence != 3 {
		t.Fatalf("counts/evidence=%#v", built)
	}
	if math.Abs(float64(built.PositiveVector[0]-.6)) > 1e-6 || math.Abs(float64(built.PositiveVector[1]-.8)) > 1e-6 {
		t.Fatalf("positive vector=%v", built.PositiveVector)
	}
	if len(built.NegativeVector) != 2 || math.Abs(float64(built.NegativeVector[0]-1)) > 1e-6 || math.Abs(float64(built.NegativeVector[1])) > 1e-6 {
		t.Fatalf("negative vector=%v", built.NegativeVector)
	}
}
