package controllers

import (
	"context"
	"errors"
	"log"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"
	"Go.exchange/recommendation"
)

const (
	recommendationProfileStatusHit          = recommendation.ProfileStatusHit
	recommendationProfileStatusStale        = recommendation.ProfileStatusStale
	recommendationProfileStatusMiss         = recommendation.ProfileStatusMiss
	recommendationProfileStatusIncompatible = recommendation.ProfileStatusIncompatible
)

func loadRecommendationProfile(ctx context.Context, repository recommendation.ProfileRepository, userID uint, servingVersion string, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	if repository == nil {
		metrics.RecordRecommendationProfileLoad("error")
		return userInterestProfile{}, errors.New("recommendation profile repository is nil")
	}
	result, err := repository.Load(ctx, recommendation.ProfileLoadQuery{
		UserID: userID, EmbeddingVersion: servingVersion,
		ExpectedProfileVersion:    recommendation.MaterializedProfileVersion,
		ExpectedProfileConfigHash: recommendation.ProfileConfigHash(cfg, servingVersion),
		Now:                       now, NegativeConfidenceHalfLifeDays: cfg.SignalHalfLifeDays,
		NegativeConfidenceSaturation: cfg.NegativeConfidenceSaturationScale,
	})
	if err != nil {
		metrics.RecordRecommendationProfileLoad("error")
		return result.Profile, err
	}
	profile := result.Profile
	metrics.RecordRecommendationProfileLoad(profile.ProfileStatus)
	if result.RecoveryError != nil {
		log.Printf("[RecommendationProfile] queue recovery user=%d reason=%s: %v", userID, result.RecoveryReason, result.RecoveryError)
		metrics.RecordRecommendationProfileLoad("error")
	}
	if profile.ProfileStatus == recommendation.ProfileStatusHit || profile.ProfileStatus == recommendation.ProfileStatusStale {
		age := time.Duration(profile.ProfileAgeMS) * time.Millisecond
		metrics.ObserveRecommendationProfileAge(age)
	}
	return profile, nil
}

func materializedNegativeConfidence(negativeEvidence float64, computedAt, now time.Time, signalHalfLifeDays, saturationScale float64, hasNegativeVector bool) float64 {
	return recommendation.MaterializedNegativeConfidence(negativeEvidence, computedAt, now, signalHalfLifeDays, saturationScale, hasNegativeVector)
}
