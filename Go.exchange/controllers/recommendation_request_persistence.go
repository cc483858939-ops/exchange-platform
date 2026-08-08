package controllers

import (
	"errors"

	"Go.exchange/global"
	"Go.exchange/models"
)

var persistRecommendationRequest = func(request models.RecommendationRequest) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return global.Db.Create(&request).Error
}

func recommendationFallbackReason(signalCount, resultCount, requestedLimit int) string {
	if signalCount == 0 {
		return "no_user_behavior"
	}
	if resultCount < requestedLimit {
		return "insufficient_candidates"
	}
	return ""
}
