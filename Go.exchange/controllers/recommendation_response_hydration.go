package controllers

import (
	"errors"
	"time"

	"Go.exchange/recommendation"
	"gorm.io/gorm"
)

func selectedRecommendationResponsesFromDB(db *gorm.DB, selected []recommendation.SelectedCandidate, now time.Time) ([]recommendedPostResponse, error) {
	if len(selected) == 0 {
		return []recommendedPostResponse{}, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	type selectedResponse struct {
		post  postResponse
		score float64
	}
	prepared := make([]selectedResponse, 0, len(selected))
	for _, item := range selected {
		post, err := postResponseFromModel(item.Post)
		if err != nil {
			continue
		}
		prepared = append(prepared, selectedResponse{post: post, score: item.Breakdown.FinalScore})
	}
	posts := make([]postResponse, 0, len(prepared))
	for _, item := range prepared {
		posts = append(posts, item.post)
	}
	if err := hydratePostResponsesMediaFromDB(db, posts); err != nil {
		return nil, err
	}
	if err := hydratePostResponseRepostCountsFromDB(db, posts); err != nil {
		return nil, err
	}
	result := make([]recommendedPostResponse, 0, len(prepared))
	for index, item := range prepared {
		post := posts[index]
		if err := hydratePostResponseReferencesFromDB(db, &post, now); err != nil {
			return nil, err
		}
		result = append(result, recommendedPostResponse{
			Post: post, Score: item.score,
		})
	}
	return result, nil
}
