package controllers

import (
	"context"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"

	"gorm.io/gorm"
)

func recommendationDBTestContext(db *gorm.DB) context.Context {
	if db != nil && db.Statement != nil && db.Statement.Context != nil {
		return db.Statement.Context
	}
	return context.Background()
}

func recommendationTestCandidateRepository(db *gorm.DB) recommendation.CandidateRepository {
	repository, _ := recommendation.NewGormCandidateRepository(db)
	return repository
}

func candidateQueryForTest(servingVersion string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) recommendation.CandidateQuery {
	return recommendation.CandidateQuery{
		UserID: userID, ServingVersion: servingVersion, Now: now, Limit: limit,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: cfg.SemanticRecall.RecentRatio,
		SemanticRecentCutoff: now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays),
		TrendingCutoff:       now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor:  cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		Served: served, SoftOnly: softOnly, MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs: profile.InteractedPostIDs,
	}
}

func loadRecommendationCandidateSet(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool) (recommendationCandidateSet, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	return buildRecommendationCandidateSet(recommendationDBTestContext(db), repository, version, userID, profile, served, now, cfg, softOnly)
}

func loadPublicRecommendationCandidateSet(db *gorm.DB, _ string, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	return buildPublicRecommendationCandidateSet(recommendationDBTestContext(db), repository, now, cfg, excluded)
}

func loadRecommendationSemanticCandidates(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadSemanticCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationSemanticPool(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, softOnly bool, cutoff time.Time, comparison string, limit int, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	query := recommendation.CandidateQuery{
		UserID: userID, ServingVersion: version, Now: now, Limit: limit,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: 0, SemanticRecentCutoff: cutoff,
		Served: served, SoftOnly: softOnly, MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs: profile.InteractedPostIDs,
	}
	_ = comparison
	candidates, err := repository.LoadSemanticCandidates(recommendationDBTestContext(db), query)
	if err != nil {
		return nil, err
	}
	if len(excluded) == 0 {
		return candidates, nil
	}
	filtered := make([]recommendation.Candidate, 0, len(candidates))
	for _, item := range candidates {
		if _, excluded := excluded[item.PostID]; !excluded {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func loadRecommendationFollowingCandidates(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadFollowingCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationSourceCandidates(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, _ interface{}, limit int, _ string) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadRecentCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationTrendingCandidates(db *gorm.DB, version string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadTrendingCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadPublicRecommendationSourceCandidates(db *gorm.DB, _ string, now time.Time, _ config.RecommendationConfig, _ interface{}, limit int, _ string, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadPublicRecentCandidates(recommendationDBTestContext(db), recommendation.PublicCandidateQuery{Now: now, Limit: limit, ExcludedPostIDs: excluded})
}

func loadPublicRecommendationTrendingCandidates(db *gorm.DB, _ string, now time.Time, cfg config.RecommendationConfig, limit int, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadPublicTrendingCandidates(recommendationDBTestContext(db), recommendation.PublicCandidateQuery{
		Now: now, Limit: limit, TrendingCutoff: now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor: cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		ExcludedPostIDs: excluded,
	})
}

func hydrateRecommendationCandidates(db *gorm.DB, version string, candidates []recommendation.Candidate, now time.Time) ([]recommendation.RankedCandidate, error) {
	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.HydrateCandidates(recommendationDBTestContext(db), version, candidates, now)
}

func loadMaterializedUserInterestProfile(db *gorm.DB, userID uint, version string, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	repository, err := recommendation.NewGormProfileRepository(db)
	if err != nil {
		return userInterestProfile{}, err
	}
	return loadRecommendationProfile(recommendationDBTestContext(db), repository, userID, version, now, cfg)
}
