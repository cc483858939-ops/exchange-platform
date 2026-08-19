package controllers

import (
	"errors"
	"log"
	"math"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"gorm.io/gorm"
)

const (
	recommendationProfileStatusHit          = "hit"
	recommendationProfileStatusStale        = "stale"
	recommendationProfileStatusMiss         = "miss"
	recommendationProfileStatusIncompatible = "incompatible"
)

func loadMaterializedUserInterestProfile(userID uint, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	if global.Db == nil {
		metrics.RecordRecommendationProfileLoad("error")
		return userInterestProfile{}, errors.New("database is not initialized")
	}
	profile := userInterestProfile{
		AuthorAffinity:       make(map[uint]float64),
		FollowingAuthorIDs:   make(map[uint]struct{}),
		InteractedArticleIDs: nil,
		ProfileStatus:        recommendationProfileStatusMiss,
	}
	var row models.UserRecoProfile
	if err := global.Db.Where("user_id = ?", userID).First(&row).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			metrics.RecordRecommendationProfileLoad("error")
			return profile, err
		}
		metrics.RecordRecommendationProfileLoad(recommendationProfileStatusMiss)
		queueMaterializedProfileRecovery(userID, "serving_miss", now)
		return profile, nil
	}

	expectedHash := recommendation.ProfileConfigHash(cfg, config.ActiveEmbeddingVersion())
	compatible := row.ProfileVersion == recommendation.MaterializedProfileVersion &&
		row.ProfileConfigHash == expectedHash && row.EmbeddingVersion == config.ActiveEmbeddingVersion()
	if !compatible {
		profile.ProfileStatus = recommendationProfileStatusIncompatible
		metrics.RecordRecommendationProfileLoad(recommendationProfileStatusIncompatible)
		queueMaterializedProfileRecovery(userID, "profile_incompatible", now)
		return profile, nil
	}
	profile.ProfileStatus = recommendationProfileStatusHit
	if !row.NextRebuildAt.After(now) {
		profile.ProfileStatus = recommendationProfileStatusStale
	}
	profile.ProfileVersion = row.ProfileVersion
	profile.ProfileConfigHash = row.ProfileConfigHash
	profile.MaterializedInteractionsReady = true
	profile.PositiveSignalCount = row.PositiveSignalCount
	profile.NegativeSignalCount = row.NegativeSignalCount
	profile.PersonalizedSignalCount = row.PersonalizedSignalCount
	if row.PositiveVector != nil {
		profile.PositiveVector = append([]float32(nil), row.PositiveVector.Slice()...)
	}
	if row.NegativeVector != nil {
		profile.NegativeVector = append([]float32(nil), row.NegativeVector.Slice()...)
	}
	if len(profile.NegativeVector) > 0 && cfg.NegativeConfidenceSaturationScale > 0 {
		profile.NegativeConfidence = math.Tanh(row.NegativeEvidence / cfg.NegativeConfidenceSaturationScale)
	}
	age := now.Sub(row.ComputedAt)
	if age < 0 {
		age = 0
	}
	profile.ProfileAgeMS = age.Milliseconds()
	metrics.RecordRecommendationProfileLoad(profile.ProfileStatus)
	metrics.ObserveRecommendationProfileAge(age)
	if profile.ProfileStatus == recommendationProfileStatusStale {
		queueMaterializedProfileRecovery(userID, "serving_stale", now)
	}
	return profile, nil
}

func queueMaterializedProfileRecovery(userID uint, reason string, now time.Time) {
	if err := recommendation.EnsureProfilesQueued(global.Db, []uint{userID}, reason, now); err != nil {
		log.Printf("[RecommendationProfile] queue recovery user=%d reason=%s: %v", userID, reason, err)
		metrics.RecordRecommendationProfileLoad("error")
	}
}
