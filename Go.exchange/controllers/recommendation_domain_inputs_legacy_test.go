package controllers

import (
	"Go.exchange/config"
	"Go.exchange/recommendation"
)

func recommendationRankingConfig(cfg config.RecommendationConfig) recommendation.RankingConfig {
	return recommendation.RankingConfig{
		SemanticWeight:         cfg.SemanticWeight,
		NegativeSemanticWeight: cfg.NegativeSemanticWeight,
		TrendingWeight:         cfg.TrendingWeight,
		AuthorAffinityWeight:   cfg.AuthorAffinityWeight,
		FollowingBonus:         cfg.FollowingBonus,
		Trending: recommendation.TrendingConfig{
			MaxAgeDays: cfg.Trending.MaxAgeDays, HalfLifeHours: cfg.Trending.HalfLifeHours,
			ReplyFactor: cfg.Trending.ReplyFactor,
		},
		Language: recommendationLanguageConfig(cfg),
	}
}

func recommendationFusionConfig(cfg config.RecommendationConfig) recommendation.FusionConfig {
	return recommendation.FusionConfig{RankConstant: cfg.Fusion.RankConstant}
}

func recommendationSelectionConfig(cfg config.RecommendationConfig) recommendation.SelectionConfig {
	return recommendation.SelectionConfig{
		OutOfNetworkMinRatio: cfg.OutOfNetworkMinRatio,
		Diversity: recommendation.DiversityConfig{
			Enabled: cfg.Diversity.Enabled, AuthorWindowSize: cfg.Diversity.AuthorWindowSize,
			MaxSameAuthorInWindow:      cfg.Diversity.MaxSameAuthorInWindow,
			SemanticDuplicateThreshold: cfg.Diversity.SemanticDuplicateThreshold,
			SemanticDuplicatePenalty:   cfg.Diversity.SemanticDuplicatePenalty,
		},
		Exploration: recommendation.ExplorationConfig{
			Ratio: cfg.Exploration.Ratio, MaxSlots: cfg.Exploration.MaxSlots,
			RecentWindowDays:    cfg.Exploration.RecentWindowDays,
			NovelPostMaxAgeDays: cfg.Exploration.NovelPostMaxAgeDays,
		},
	}
}

func recommendationLanguageConfig(cfg config.RecommendationConfig) recommendation.LanguageConfig {
	return recommendation.LanguageConfig{
		Enabled: cfg.LanguageAffinity.Enabled, Weight: cfg.LanguageAffinity.Weight,
		EvidenceSaturationScale: cfg.LanguageAffinity.EvidenceSaturationScale,
		MaxBehaviorShare:        cfg.LanguageAffinity.MaxBehaviorShare,
	}
}

func recommendationProfileFeatures(profile userInterestProfile) recommendation.ProfileFeatures {
	return recommendation.ProfileFeatures{
		PositiveVector: profile.PositiveVector, NegativeVector: profile.NegativeVector,
		NegativeConfidence: profile.NegativeConfidence, AuthorAffinity: profile.AuthorAffinity,
		FollowingAuthorIDs: profile.FollowingAuthorIDs,
	}
}
