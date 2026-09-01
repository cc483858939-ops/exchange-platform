package recommendation

import (
	"errors"
	"math"
	"time"

	"Go.exchange/config"
)

type InterestProfile struct {
	PositiveVector                []float32
	NegativeVector                []float32
	NegativeEvidence              float64
	PositiveSignalCount           int
	NegativeSignalCount           int
	PersonalizedSignalCount       int
	PositiveContributions         map[uint]float64
	PositiveAffinityContributions map[uint]float64
	InteractedPostIDs             []uint
}

type EmbeddingLoader func(postIDs []uint, version string) (map[uint][]float32, error)

// BuildInterestProfile is the single production implementation of the
// current embedding profile math. It intentionally receives canonical input
// and an embedding loader so materialization and focused tests share the same
// calculation without introducing event-level vector deltas.
func BuildInterestProfile(canonical CanonicalizationResult, now time.Time, cfg config.RecommendationConfig, embeddingVersion string, load EmbeddingLoader) (InterestProfile, error) {
	if load == nil {
		return InterestProfile{}, errors.New("embedding loader is nil")
	}
	if cfg.PositivePostWeightCap <= 0 {
		cfg.PositivePostWeightCap = 7
	}
	if cfg.NegativeConfidenceSaturationScale <= 0 {
		cfg.NegativeConfidenceSaturationScale = 12
	}
	if cfg.SignalHalfLifeDays <= 0 {
		cfg.SignalHalfLifeDays = 14
	}
	profile := InterestProfile{
		PositiveContributions:         make(map[uint]float64),
		PositiveAffinityContributions: make(map[uint]float64),
		InteractedPostIDs:             append([]uint(nil), canonical.InteractedPostIDs...),
	}
	ids := make([]uint, 0, len(canonical.Outcomes))
	for _, outcome := range canonical.Outcomes {
		ids = append(ids, outcome.PostID)
	}
	embeddingsByPost, err := load(ids, embeddingVersion)
	if err != nil {
		return profile, err
	}
	for _, outcome := range canonical.Outcomes {
		positiveStrength := PositivePostStrength(outcome, now, cfg)
		if positiveStrength > 0 {
			profile.PositiveContributions[outcome.PostID] = positiveStrength
			if affinityStrength := AuthorAffinityContribution(outcome, now, cfg); affinityStrength > 0 {
				profile.PositiveAffinityContributions[outcome.PostID] = affinityStrength
			}
		}
		vector := embeddingsByPost[outcome.PostID]
		if !ValidEmbeddingVector(vector) {
			continue
		}
		if positiveStrength > 0 && AddEmbeddingContribution(&profile.PositiveVector, vector, positiveStrength) {
			profile.PositiveSignalCount++
		}
		negativeStrength := NegativePostStrength(outcome, now, cfg)
		if negativeStrength > 0 && AddEmbeddingContribution(&profile.NegativeVector, vector, negativeStrength) {
			profile.NegativeEvidence += negativeStrength
			profile.NegativeSignalCount++
		}
	}
	profile.PositiveVector = NormalizeEmbedding(profile.PositiveVector)
	profile.NegativeVector = NormalizeEmbedding(profile.NegativeVector)
	profile.PersonalizedSignalCount = profile.PositiveSignalCount + profile.NegativeSignalCount
	return profile, nil
}

func AddEmbeddingContribution(target *[]float32, vector []float32, strength float64) bool {
	if strength <= 0 || !ValidEmbeddingVector(vector) {
		return false
	}
	if len(*target) == 0 {
		*target = make([]float32, len(vector))
	}
	if len(*target) != len(vector) {
		return false
	}
	for index, value := range vector {
		(*target)[index] += float32(float64(value) * strength)
	}
	return true
}

func PositivePostStrength(outcome UserPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if len(outcome.PositiveSignals) > 0 {
		decayed := make([]float64, len(outcome.PositiveSignals))
		primaryIndex, primary := 0, 0.0
		for index, signal := range outcome.PositiveSignals {
			decayed[index] = EmbeddingSignalWeight(cfg, signal.SignalType) * SignalDecay(now, signal.OccurredAt, cfg.SignalHalfLifeDays)
			if decayed[index] > primary {
				primaryIndex, primary = index, decayed[index]
			}
		}
		coexist := 0.0
		for index := range outcome.PositiveSignals {
			if index != primaryIndex {
				coexist += cfg.PositiveSignalCoexistBonus * SignalDecay(now, outcome.PositiveSignals[index].OccurredAt, cfg.SignalHalfLifeDays)
			}
		}
		return math.Min(cfg.PositivePostWeightCap, primary+coexist)
	}
	if outcome.PassiveSignal == nil {
		return 0
	}
	if outcome.PassiveSignal.SignalType == "quick_bounce" || outcome.PassiveSignal.SignalType == "neutral_read" || outcome.PassiveSignal.SignalType == "not_interested" {
		return 0
	}
	return math.Max(0, EmbeddingSignalWeight(cfg, outcome.PassiveSignal.SignalType)*SignalDecay(now, outcome.PassiveSignal.OccurredAt, cfg.SignalHalfLifeDays))
}

func AuthorAffinityContribution(outcome UserPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if len(outcome.PositiveSignals) > 0 {
		return PositivePostStrength(outcome, now, cfg)
	}
	if outcome.PassiveSignal == nil {
		return 0
	}
	if outcome.PassiveSignal.SignalType != "click" && outcome.PassiveSignal.SignalType != "qualified_read" {
		return 0
	}
	return math.Max(0, EmbeddingSignalWeight(cfg, outcome.PassiveSignal.SignalType)*SignalDecay(now, outcome.PassiveSignal.OccurredAt, cfg.SignalHalfLifeDays))
}

func NegativePostStrength(outcome UserPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if outcome.NegativeSignal == nil {
		return 0
	}
	if outcome.NegativeSignal.SignalType != "quick_bounce" && outcome.NegativeSignal.SignalType != "not_interested" {
		return 0
	}
	return math.Abs(EmbeddingSignalWeight(cfg, outcome.NegativeSignal.SignalType)) * SignalDecay(now, outcome.NegativeSignal.OccurredAt, cfg.SignalHalfLifeDays)
}

func ValidEmbeddingVector(vector []float32) bool {
	if len(vector) == 0 {
		return false
	}
	hasNonZero := false
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		if value != 0 {
			hasNonZero = true
		}
	}
	return hasNonZero
}

func NormalizeEmbedding(vector []float32) []float32 {
	if !ValidEmbeddingVector(vector) {
		return nil
	}
	norm := 0.0
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 {
		return nil
	}
	length := float32(math.Sqrt(norm))
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = value / length
	}
	return result
}

func EmbeddingSignalWeight(cfg config.RecommendationConfig, signal string) float64 {
	switch signal {
	case "view":
		return cfg.BehaviorWeights.View
	case "click":
		return cfg.BehaviorWeights.Click
	case "qualified_read":
		return cfg.BehaviorWeights.QualifiedRead
	case "reply":
		return cfg.BehaviorWeights.Reply
	case "quick_bounce":
		return cfg.BehaviorWeights.QuickBounce
	case "like":
		return cfg.BehaviorWeights.Like
	case "not_interested":
		return cfg.BehaviorWeights.NotInterested
	default:
		return 0
	}
}

func SignalDecay(now, occurred time.Time, halfLifeDays float64) float64 {
	if occurred.IsZero() || occurred.After(now) || halfLifeDays <= 0 {
		return 1
	}
	age := now.Sub(occurred).Hours() / 24
	return math.Exp(-math.Ln2 * age / halfLifeDays)
}

func CosineSimilarity(left, right []float32) float64 {
	if !ValidEmbeddingVector(left) || !ValidEmbeddingVector(right) || len(left) != len(right) {
		return 0
	}
	dot, leftNorm, rightNorm := 0.0, 0.0, 0.0
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
		leftNorm += float64(left[index]) * float64(left[index])
		rightNorm += float64(right[index]) * float64(right[index])
	}
	if leftNorm <= 0 || rightNorm <= 0 {
		return 0
	}
	value := dot / math.Sqrt(leftNorm*rightNorm)
	if value > 1 {
		return 1
	}
	if value < -1 {
		return -1
	}
	return value
}
