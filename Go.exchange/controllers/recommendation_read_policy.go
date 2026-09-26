package controllers

import (
	"time"

	"Go.exchange/recommendation"
)

const (
	recommendationReadPolicyVersion                = recommendation.RecommendationReadPolicyVersion
	recommendationReadOutcomeQualified             = "qualified"
	recommendationReadOutcomeQuickBounce           = "quick_bounce"
	recommendationReadOutcomeNeutral               = "neutral"
	recommendationReadMinimumDwellMS         int64 = 3 * 1000
	recommendationReadMaxForegroundMS        int64 = 6 * 60 * 60 * 1000
	recommendationReadMaxProgress                  = 100
	recommendationReadMinimumProgress              = 50
	recommendationReadQuickBounceProgress          = 10
	recommendationReadCJKCharactersPerMinute       = 300
	recommendationReadLatinWordsPerMinute          = 220
	recommendationReadMinimumEstimateMS      int64 = 3 * 1000
	recommendationReadMaximumEstimateMS      int64 = 120 * 1000
)

func classifyRecommendationRead(foregroundTimeMS int64, scrollProgressPercent int, estimatedReadTimeMS int64, readPolicyVersion string) (string, error) {
	return recommendation.ClassifyRecommendationRead(foregroundTimeMS, scrollProgressPercent, estimatedReadTimeMS, readPolicyVersion)
}

func estimatePostReadTime(content string) time.Duration {
	return recommendation.EstimatePostReadTime(content)
}

func recommendationReadPolicyVersionValue() string {
	return recommendation.RecommendationReadPolicyVersion
}

func recommendationReadOutcomeIsValid(outcome string) bool {
	switch outcome {
	case recommendationReadOutcomeQualified, recommendationReadOutcomeQuickBounce, recommendationReadOutcomeNeutral:
		return true
	default:
		return false
	}
}
