package controllers

import (
	"context"
	"errors"
	"time"

	"Go.exchange/models"
	"Go.exchange/recommendation"
)

const recommendationTracePersistTimeout = 5 * time.Second

func persistRecommendationServingTraceViaRepository(parent context.Context, repository recommendation.TraceRepository, request models.RecommendationRequest, results []models.RecommendationResultTrace) error {
	if repository == nil {
		return errors.New("recommendation trace repository is nil")
	}
	if parent == nil {
		return errors.New("recommendation trace context is nil")
	}
	ctx, cancel := context.WithTimeout(parent, recommendationTracePersistTimeout)
	defer cancel()
	return repository.PersistServing(ctx, request, results)
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
