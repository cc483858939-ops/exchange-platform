package controllers

import (
	"context"
	"errors"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"
)

type servedPost = recommendation.ServedItem

type recommendationCandidateSet struct {
	Candidates     []recommendation.Candidate
	SemanticCount  int
	FollowingCount int
	RecentCount    int
	RecentPostIDs  []uint
	TrendingCount  int
}

func buildRecommendationCandidateSet(ctx context.Context, repository recommendation.CandidateRepository, servingVersion string, userID uint, profile userInterestProfile, served recommendation.ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool) (recommendationCandidateSet, error) {
	if repository == nil {
		return recommendationCandidateSet{}, errors.New("recommendation candidate repository is nil")
	}
	caps := recommendationCandidateCaps(profile, cfg)
	query := recommendation.CandidateQuery{
		UserID: userID, ServingVersion: servingVersion, Now: now,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: cfg.SemanticRecall.RecentRatio,
		SemanticRecentCutoff: now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays),
		TrendingCutoff:       now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor:  cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		Served: served, SoftOnly: softOnly,
		MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs:             profile.InteractedPostIDs,
	}
	query.Limit = caps.Semantic
	semantic, err := repository.LoadSemanticCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	query.Limit = caps.Following
	following, err := repository.LoadFollowingCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	query.Limit = caps.Recent
	recent, err := repository.LoadRecentCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	query.Limit = caps.Trending
	trending, err := repository.LoadTrendingCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	merged := recommendation.FuseCandidates(
		caps.Merged, recommendationFusionConfig(cfg),
		recommendation.CandidateSet{Source: recommendation.CandidateSourceSemantic, Candidates: semantic},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceFollowing, Candidates: following},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceRecent, Candidates: recent},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceTrending, Candidates: trending},
	)
	for index := range merged {
		if item, ok := served[merged[index].PostID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	return recommendationCandidateSet{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), RecentPostIDs: recommendation.CandidatePostIDs(recent), TrendingCount: len(trending),
	}, nil
}

func buildPublicRecommendationCandidateSet(ctx context.Context, repository recommendation.CandidateRepository, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
	if repository == nil {
		return recommendationCandidateSet{}, errors.New("recommendation candidate repository is nil")
	}
	caps := cfg.Candidates.ColdStart
	query := recommendation.PublicCandidateQuery{
		Now: now, TrendingCutoff: now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor: cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		ExcludedPostIDs: excluded,
	}
	query.Limit = caps.Recent
	recent, err := repository.LoadPublicRecentCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	query.Limit = caps.Trending
	trending, err := repository.LoadPublicTrendingCandidates(ctx, query)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	merged := recommendation.FuseCandidates(
		caps.Merged, recommendationFusionConfig(cfg),
		recommendation.CandidateSet{Source: recommendation.CandidateSourceRecent, Candidates: recent},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceTrending, Candidates: trending},
	)
	return recommendationCandidateSet{Candidates: merged, RecentCount: len(recent), RecentPostIDs: recommendation.CandidatePostIDs(recent), TrendingCount: len(trending)}, nil
}

func recommendationCandidateCaps(profile userInterestProfile, cfg config.RecommendationConfig) config.RecommendationCandidateCaps {
	if len(profile.PositiveVector) == 0 {
		return cfg.Candidates.ColdStart
	}
	return cfg.Candidates.Personalized
}

func mergeCandidateSets(first, second recommendationCandidateSet, mergedLimit int) recommendationCandidateSet {
	recentPostIDs := append([]uint(nil), first.RecentPostIDs...)
	seenRecent := make(map[uint]struct{}, len(recentPostIDs))
	for _, postID := range recentPostIDs {
		seenRecent[postID] = struct{}{}
	}
	for _, postID := range second.RecentPostIDs {
		if _, exists := seenRecent[postID]; exists {
			continue
		}
		seenRecent[postID] = struct{}{}
		recentPostIDs = append(recentPostIDs, postID)
	}
	return recommendationCandidateSet{
		Candidates:     recommendation.MergeCandidates(mergedLimit, first.Candidates, second.Candidates),
		SemanticCount:  first.SemanticCount + second.SemanticCount,
		FollowingCount: first.FollowingCount + second.FollowingCount,
		RecentCount:    first.RecentCount + second.RecentCount,
		RecentPostIDs:  recentPostIDs,
		TrendingCount:  first.TrendingCount + second.TrendingCount,
	}
}
