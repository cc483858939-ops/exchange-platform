package controllers

import (
	"errors"

	"Go.exchange/global"
	"Go.exchange/recommendation"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func newRecommendationDataDependencies(db *gorm.DB, redisClient *redis.Client) (recommendation.DataDependencies, error) {
	candidates, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return recommendation.DataDependencies{}, err
	}
	profiles, err := recommendation.NewGormProfileRepository(db)
	if err != nil {
		return recommendation.DataDependencies{}, err
	}
	traces, err := recommendation.NewGormTraceRepository(db)
	if err != nil {
		return recommendation.DataDependencies{}, err
	}
	history, err := newRecommendationHistoryStore(redisClient)
	if err != nil {
		return recommendation.DataDependencies{}, err
	}
	return recommendation.DataDependencies{Candidates: candidates, Profiles: profiles, History: history, Traces: traces}, nil
}

func newRecommendationHistoryStore(client *redis.Client) (recommendation.HistoryStore, error) {
	if client == nil {
		return nil, nil
	}
	store, err := recommendation.NewRedisHistoryStore(client)
	if err != nil {
		return nil, err
	}
	return store, nil
}

func recommendationAPIDatabase() (*gorm.DB, error) {
	if global.APIDb != nil {
		return global.APIDb, nil
	}
	if global.Db != nil {
		return global.Db, nil
	}
	return nil, errors.New("database is not initialized")
}
