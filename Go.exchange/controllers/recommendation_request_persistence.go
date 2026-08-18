package controllers

import (
	"context"
	"errors"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"gorm.io/gorm"
)

const recommendationTracePersistTimeout = 5 * time.Second

var persistRecommendationServingTrace = persistRecommendationServingTraceToDB

func persistRecommendationServingTraceToDB(request models.RecommendationRequest, results []models.RecommendationResultTrace) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), recommendationTracePersistTimeout)
	defer cancel()
	return global.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		if len(results) == 0 {
			return nil
		}
		return tx.Create(&results).Error
	})
}

func recommendationFallbackReason(signalCount, resultCount, requestedLimit int) string {
	if signalCount == 0 {
		return "no_positive_profile"
	}
	if resultCount < requestedLimit {
		return "insufficient_fresh_candidates"
	}
	return ""
}
