package controllers

import (
	"context"
	"time"

	"Go.exchange/global"
	"Go.exchange/recommendation"
)

func selectedRecommendationResponses(ctx context.Context, selected []recommendation.SelectedCandidate) ([]recommendedPostResponse, error) {
	db := global.Db
	if db != nil {
		db = db.WithContext(ctx)
	}
	return selectedRecommendationResponsesFromDB(db, selected, time.Now().UTC())
}
