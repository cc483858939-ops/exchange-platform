package router

import (
	"context"
	"testing"
	"time"

	"Go.exchange/controllers"
	"Go.exchange/recommendation"
)

type routerRecommendationServiceFake struct{}

func (routerRecommendationServiceFake) Serve(_ context.Context, _ recommendation.ServeRequest) (recommendation.ServeResult, error) {
	return recommendation.ServeResult{Now: time.Now().UTC()}, nil
}

type routerRecommendationResponseMapperFake struct{}

func (routerRecommendationResponseMapperFake) Map(context.Context, []recommendation.SelectedCandidate, time.Time) ([]controllers.RecommendedPostResponse, error) {
	return []controllers.RecommendedPostResponse{}, nil
}

func newRouterRecommendationHandler(t *testing.T) *controllers.RecommendationHandler {
	t.Helper()
	handler, err := controllers.NewRecommendationHandler(
		routerRecommendationServiceFake{},
		routerRecommendationResponseMapperFake{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
