package controllers

import (
	"Go.exchange/config"
	"Go.exchange/recommendation"
)

const (
	recommendationScene                   = "recommendation_page"
	recommendationPersonalizedStrategyID  = recommendation.RecommendationPersonalizedStrategyID
	recommendationColdStartStrategyID     = recommendation.RecommendationColdStartStrategyID
	recommendationTrackingTokenVersion    = recommendation.RecommendationTrackingTokenVersion
	recommendationCanonicalOutcomeVersion = recommendation.CanonicalOutcomeVersion
	recommendationPassiveRecencyPolicy    = recommendation.RecommendationPassiveRecencyPolicy
	recommendationSigningKeyMinBytes      = 32
)

type recommendationTrackingClaims = recommendation.TrackingClaims

func recommendationStrategyID(profile userInterestProfile) string {
	return recommendation.StrategyID(profile)
}

func recommendationTelemetryRequestSelected(userID uint, requestID string, percent int) bool {
	return recommendation.RecommendationTelemetryRequestSelected(userID, requestID, percent)
}

func recommendationRankerConfigHash(cfg config.RecommendationConfig, servingVersion string) string {
	return recommendation.RankerConfigHash(cfg, servingVersion)
}

func signRecommendationTrackingClaims(claims recommendationTrackingClaims, key []byte) (string, error) {
	return recommendation.SignTrackingClaims(claims, key)
}

func verifyRecommendationTrackingToken(token string, key []byte) (recommendationTrackingClaims, error) {
	return recommendation.VerifyTrackingToken(token, key)
}
