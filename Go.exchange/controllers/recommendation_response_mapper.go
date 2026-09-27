package controllers

import (
	"context"
	"errors"
	"time"

	"Go.exchange/recommendation"
	"gorm.io/gorm"
)

type RecommendationResponseMapper interface {
	Map(ctx context.Context, selected []recommendation.SelectedCandidate, now time.Time) ([]RecommendedPostResponse, error)
}

type GormRecommendationResponseMapper struct {
	db *gorm.DB
}

func NewGormRecommendationResponseMapper(db *gorm.DB) (*GormRecommendationResponseMapper, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &GormRecommendationResponseMapper{db: db}, nil
}

func (mapper *GormRecommendationResponseMapper) Map(ctx context.Context, selected []recommendation.SelectedCandidate, now time.Time) ([]RecommendedPostResponse, error) {
	if len(selected) == 0 {
		return []RecommendedPostResponse{}, nil
	}
	if mapper == nil || mapper.db == nil {
		return nil, errors.New("database is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("request context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
	if len(prepared) == 0 {
		return []RecommendedPostResponse{}, nil
	}
	db := mapper.db.WithContext(ctx)
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
	result := make([]RecommendedPostResponse, 0, len(prepared))
	for index, item := range prepared {
		post := posts[index]
		if err := hydratePostResponseReferencesFromDB(db, &post, now); err != nil {
			return nil, err
		}
		result = append(result, RecommendedPostResponse{
			Post: post, Score: item.score,
		})
	}
	return result, nil
}
