package recommendation

import (
	"math"
	"strings"

	"Go.exchange/config"
)

func DefaultConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
		ServingTimeoutMS: 5000,
		BehaviorWeights: config.RecommendationBehaviorWeights{
			View: 0.25, Like: 4, Click: 1, QualifiedRead: 2.5, Reply: 5, QuickBounce: -2, NotInterested: -8,
		},
		SemanticRecall:     config.RecommendationSemanticRecallConfig{RecentWindowDays: 7, RecentRatio: 0.85},
		Fusion:             config.RecommendationFusionConfig{RankConstant: 60},
		Trending:           config.RecommendationTrendingConfig{MaxAgeDays: 3, HalfLifeHours: 12, ReplyFactor: 1.5},
		Exploration:        config.RecommendationExplorationConfig{Ratio: 0.10, MaxSlots: 3, RecentWindowDays: 7, NovelPostMaxAgeDays: 30},
		LanguageAffinity:   config.RecommendationLanguageAffinityConfig{Enabled: true, Weight: 0.35, EvidenceSaturationScale: 5, MaxBehaviorShare: 0.95},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90,
		PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
		SemanticWeight: 4, NegativeSemanticWeight: 1.5, NegativeConfidenceSaturationScale: 12,
		TrendingWeight:       0.5,
		AuthorAffinityWeight: 1, AuthorAffinitySaturationScale: 6, FollowingBonus: 0.5,
		OutOfNetworkMinRatio:       0.30,
		ServedHardExclusionMinutes: 30, ServedSoftLookbackDays: 7, ServedHistoryLimit: 1000, GuestServedHistoryLimit: 2000, GuestServedHistoryTTLHours: 24,
		Diversity: config.RecommendationDiversityConfig{
			Enabled: true, AuthorWindowSize: 8, MaxSameAuthorInWindow: 2,
			SemanticDuplicateThreshold: 0.92, SemanticDuplicatePenalty: 1,
		},
		Trace: config.RecommendationTraceConfig{
			ResultRetentionDays: 30, RequestRetentionDays: 90, CleanupIntervalHours: 6, CleanupBatchSize: 5000,
			PersistTimeoutMS: defaultTracePersistTimeoutMS, QueueCapacity: defaultTraceQueueCapacity,
			WorkerCount: defaultTraceWorkerCount, ShutdownDrainTimeoutMS: defaultTraceShutdownDrainTimeoutMS,
		},
		Candidates: config.RecommendationCandidatesConfig{
			Personalized: config.RecommendationCandidateCaps{Semantic: 200, Following: 150, Recent: 150, Trending: 150, Merged: 500},
			ColdStart:    config.RecommendationCandidateCaps{Following: 200, Recent: 200, Trending: 200, Merged: 500},
		},
		ProfileMaterialization: config.RecommendationProfileMaterializationConfig{
			DebounceSeconds:          config.DefaultRecommendationProfileDebounceSeconds,
			PollIntervalSeconds:      config.DefaultRecommendationProfilePollIntervalSeconds,
			BatchSize:                config.DefaultRecommendationProfileBatchSize,
			RebuildIntervalHours:     config.DefaultRecommendationProfileRebuildIntervalHours,
			StaleScanIntervalSeconds: config.DefaultRecommendationProfileStaleScanIntervalSeconds,
			StaleEnqueueBatchSize:    config.DefaultRecommendationProfileStaleEnqueueBatchSize,
		},
	}
}

func NormalizeConfig(configured config.RecommendationConfig, presence map[string]bool) config.RecommendationConfig {
	cfg := DefaultConfig()
	set := configured
	if set.ServingTimeoutMS > 0 {
		cfg.ServingTimeoutMS = set.ServingTimeoutMS
	}
	if settingProvided(presence, "behavior_weights.view", set.BehaviorWeights.View != 0) {
		cfg.BehaviorWeights.View = set.BehaviorWeights.View
	}
	if settingProvided(presence, "behavior_weights.like", set.BehaviorWeights.Like != 0) {
		cfg.BehaviorWeights.Like = set.BehaviorWeights.Like
	}
	if settingProvided(presence, "behavior_weights.click", set.BehaviorWeights.Click != 0) {
		cfg.BehaviorWeights.Click = set.BehaviorWeights.Click
	}
	if settingProvided(presence, "behavior_weights.qualified_read", set.BehaviorWeights.QualifiedRead != 0) {
		cfg.BehaviorWeights.QualifiedRead = set.BehaviorWeights.QualifiedRead
	}
	if set.BehaviorWeights.Reply >= 0 && settingProvided(presence, "behavior_weights.reply", set.BehaviorWeights.Reply > 0) {
		cfg.BehaviorWeights.Reply = set.BehaviorWeights.Reply
	}
	if settingProvided(presence, "behavior_weights.quick_bounce", set.BehaviorWeights.QuickBounce != 0) {
		cfg.BehaviorWeights.QuickBounce = set.BehaviorWeights.QuickBounce
	}
	if settingProvided(presence, "behavior_weights.not_interested", set.BehaviorWeights.NotInterested != 0) {
		cfg.BehaviorWeights.NotInterested = set.BehaviorWeights.NotInterested
	}
	if set.SignalHalfLifeDays > 0 {
		cfg.SignalHalfLifeDays = set.SignalHalfLifeDays
	}
	if set.FeedbackLookbackDays > 0 {
		cfg.FeedbackLookbackDays = set.FeedbackLookbackDays
	}
	if set.Fusion.RankConstant > 0 {
		cfg.Fusion.RankConstant = set.Fusion.RankConstant
	}
	if set.PositiveSignalCoexistBonus >= 0 && settingProvided(presence, "positive_signal_coexist_bonus", set.PositiveSignalCoexistBonus != 0) {
		cfg.PositiveSignalCoexistBonus = set.PositiveSignalCoexistBonus
	}
	if set.PositivePostWeightCap > 0 {
		cfg.PositivePostWeightCap = set.PositivePostWeightCap
	}
	if set.SemanticWeight >= 0 && settingProvided(presence, "semantic_weight", set.SemanticWeight != 0) {
		cfg.SemanticWeight = set.SemanticWeight
	}
	if set.NegativeSemanticWeight >= 0 && settingProvided(presence, "negative_semantic_weight", set.NegativeSemanticWeight != 0) {
		cfg.NegativeSemanticWeight = set.NegativeSemanticWeight
	}
	if set.NegativeConfidenceSaturationScale > 0 {
		cfg.NegativeConfidenceSaturationScale = set.NegativeConfidenceSaturationScale
	}
	if settingProvided(presence, "trending_weight", set.TrendingWeight != 0) {
		cfg.TrendingWeight = set.TrendingWeight
	}
	if settingProvided(presence, "trending.reply_factor", set.Trending.ReplyFactor != 0) {
		cfg.Trending.ReplyFactor = set.Trending.ReplyFactor
	}
	if set.SemanticRecall.RecentWindowDays > 0 {
		cfg.SemanticRecall.RecentWindowDays = set.SemanticRecall.RecentWindowDays
	}
	if settingProvided(presence, "semantic_recall.recent_ratio", set.SemanticRecall.RecentRatio != 0) {
		cfg.SemanticRecall.RecentRatio = set.SemanticRecall.RecentRatio
	}
	if set.Trending.MaxAgeDays > 0 {
		cfg.Trending.MaxAgeDays = set.Trending.MaxAgeDays
	}
	if set.Trending.HalfLifeHours > 0 {
		cfg.Trending.HalfLifeHours = set.Trending.HalfLifeHours
	}
	if settingProvided(presence, "exploration.ratio", set.Exploration.Ratio != 0) {
		cfg.Exploration.Ratio = set.Exploration.Ratio
	}
	if set.Exploration.MaxSlots > 0 {
		cfg.Exploration.MaxSlots = set.Exploration.MaxSlots
	}
	if set.Exploration.RecentWindowDays > 0 {
		cfg.Exploration.RecentWindowDays = set.Exploration.RecentWindowDays
	}
	if set.Exploration.NovelPostMaxAgeDays > 0 {
		cfg.Exploration.NovelPostMaxAgeDays = set.Exploration.NovelPostMaxAgeDays
	}
	if set.AuthorAffinityWeight >= 0 && settingProvided(presence, "author_affinity_weight", set.AuthorAffinityWeight != 0) {
		cfg.AuthorAffinityWeight = set.AuthorAffinityWeight
	}
	if set.AuthorAffinitySaturationScale > 0 {
		cfg.AuthorAffinitySaturationScale = set.AuthorAffinitySaturationScale
	}
	if set.FollowingBonus >= 0 && settingProvided(presence, "following_bonus", set.FollowingBonus != 0) {
		cfg.FollowingBonus = set.FollowingBonus
	}
	if set.OutOfNetworkMinRatio >= 0 && set.OutOfNetworkMinRatio <= 1 && settingProvided(presence, "out_of_network_min_ratio", set.OutOfNetworkMinRatio != 0) {
		cfg.OutOfNetworkMinRatio = set.OutOfNetworkMinRatio
	}
	if set.ServedHardExclusionMinutes > 0 {
		cfg.ServedHardExclusionMinutes = set.ServedHardExclusionMinutes
	}
	if set.ServedSoftLookbackDays > 0 {
		cfg.ServedSoftLookbackDays = set.ServedSoftLookbackDays
	}
	if set.ServedHistoryLimit > 0 {
		cfg.ServedHistoryLimit = set.ServedHistoryLimit
	}
	if set.GuestServedHistoryLimit > 0 {
		cfg.GuestServedHistoryLimit = set.GuestServedHistoryLimit
	}
	if set.GuestServedHistoryTTLHours > 0 {
		cfg.GuestServedHistoryTTLHours = set.GuestServedHistoryTTLHours
	}
	if settingProvided(presence, "diversity.enabled", set.Diversity.Enabled) {
		cfg.Diversity.Enabled = set.Diversity.Enabled
	}
	if set.Diversity.AuthorWindowSize > 0 {
		cfg.Diversity.AuthorWindowSize = set.Diversity.AuthorWindowSize
	}
	if set.Diversity.MaxSameAuthorInWindow > 0 {
		cfg.Diversity.MaxSameAuthorInWindow = set.Diversity.MaxSameAuthorInWindow
	}
	if set.Diversity.SemanticDuplicateThreshold >= -1 && set.Diversity.SemanticDuplicateThreshold <= 1 && settingProvided(presence, "diversity.semantic_duplicate_threshold", set.Diversity.SemanticDuplicateThreshold != 0) {
		cfg.Diversity.SemanticDuplicateThreshold = set.Diversity.SemanticDuplicateThreshold
	}
	if set.Diversity.SemanticDuplicatePenalty >= 0 && settingProvided(presence, "diversity.semantic_duplicate_penalty", set.Diversity.SemanticDuplicatePenalty != 0) {
		cfg.Diversity.SemanticDuplicatePenalty = set.Diversity.SemanticDuplicatePenalty
	}
	if settingProvided(presence, "language_affinity.enabled", set.LanguageAffinity.Enabled) {
		cfg.LanguageAffinity.Enabled = set.LanguageAffinity.Enabled
	}
	if settingProvided(presence, "language_affinity.weight", set.LanguageAffinity.Weight != 0) {
		cfg.LanguageAffinity.Weight = set.LanguageAffinity.Weight
	}
	if settingProvided(presence, "language_affinity.evidence_saturation_scale", set.LanguageAffinity.EvidenceSaturationScale != 0) {
		cfg.LanguageAffinity.EvidenceSaturationScale = set.LanguageAffinity.EvidenceSaturationScale
	}
	if settingProvided(presence, "language_affinity.max_behavior_share", set.LanguageAffinity.MaxBehaviorShare != 0) {
		cfg.LanguageAffinity.MaxBehaviorShare = set.LanguageAffinity.MaxBehaviorShare
	}
	if set.Trace.ResultRetentionDays > 0 {
		cfg.Trace.ResultRetentionDays = set.Trace.ResultRetentionDays
	}
	if set.Trace.RequestRetentionDays > 0 {
		cfg.Trace.RequestRetentionDays = set.Trace.RequestRetentionDays
	}
	if set.Trace.CleanupIntervalHours > 0 {
		cfg.Trace.CleanupIntervalHours = set.Trace.CleanupIntervalHours
	}
	if set.Trace.CleanupBatchSize > 0 {
		cfg.Trace.CleanupBatchSize = set.Trace.CleanupBatchSize
	}
	if set.Trace.PersistTimeoutMS > 0 {
		cfg.Trace.PersistTimeoutMS = set.Trace.PersistTimeoutMS
	}
	if set.Trace.QueueCapacity > 0 {
		cfg.Trace.QueueCapacity = set.Trace.QueueCapacity
	}
	if set.Trace.WorkerCount > 0 {
		cfg.Trace.WorkerCount = set.Trace.WorkerCount
	}
	if set.Trace.ShutdownDrainTimeoutMS > 0 {
		cfg.Trace.ShutdownDrainTimeoutMS = set.Trace.ShutdownDrainTimeoutMS
	}
	applyCandidateCaps(&cfg.Candidates.Personalized, set.Candidates.Personalized)
	applyCandidateCaps(&cfg.Candidates.ColdStart, set.Candidates.ColdStart)
	cfg.ProfileMaterialization = set.ProfileMaterialization.Normalized()

	if cfg.BehaviorWeights.Reply < 0 {
		cfg.BehaviorWeights.Reply = 5
	}
	if cfg.PositiveSignalCoexistBonus < 0 {
		cfg.PositiveSignalCoexistBonus = 1
	}
	if cfg.PositivePostWeightCap <= 0 || cfg.PositivePostWeightCap < math.Max(cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Reply) {
		cfg.PositivePostWeightCap = 7
	}
	if cfg.NegativeSemanticWeight < 0 {
		cfg.NegativeSemanticWeight = 1.5
	}
	if cfg.NegativeConfidenceSaturationScale <= 0 {
		cfg.NegativeConfidenceSaturationScale = 12
	}
	if cfg.AuthorAffinityWeight < 0 {
		cfg.AuthorAffinityWeight = 1
	}
	if cfg.AuthorAffinitySaturationScale <= 0 {
		cfg.AuthorAffinitySaturationScale = 6
	}
	if cfg.FollowingBonus < 0 {
		cfg.FollowingBonus = 0.5
	}
	if cfg.OutOfNetworkMinRatio < 0 || cfg.OutOfNetworkMinRatio > 1 {
		cfg.OutOfNetworkMinRatio = 0.30
	}
	if cfg.Exploration.Ratio < 0 || cfg.Exploration.Ratio > 0.25 {
		cfg.Exploration.Ratio = 0.10
	}
	if cfg.Exploration.MaxSlots <= 0 {
		cfg.Exploration.MaxSlots = 3
	}
	if cfg.Exploration.RecentWindowDays <= 0 {
		cfg.Exploration.RecentWindowDays = 7
	}
	if cfg.Exploration.NovelPostMaxAgeDays <= 0 {
		cfg.Exploration.NovelPostMaxAgeDays = 30
	}
	if cfg.ServedHardExclusionMinutes <= 0 {
		cfg.ServedHardExclusionMinutes = 30
	}
	if cfg.ServedSoftLookbackDays <= 0 {
		cfg.ServedSoftLookbackDays = 7
	}
	if cfg.ServedHistoryLimit <= 0 {
		cfg.ServedHistoryLimit = 1000
	}
	if cfg.GuestServedHistoryLimit <= 0 {
		cfg.GuestServedHistoryLimit = 2000
	}
	if cfg.GuestServedHistoryTTLHours <= 0 {
		cfg.GuestServedHistoryTTLHours = 24
	}
	if cfg.Diversity.AuthorWindowSize <= 0 {
		cfg.Diversity.AuthorWindowSize = 8
	}
	if cfg.Diversity.MaxSameAuthorInWindow <= 0 {
		cfg.Diversity.MaxSameAuthorInWindow = 2
	}
	if cfg.Diversity.SemanticDuplicateThreshold < -1 || cfg.Diversity.SemanticDuplicateThreshold > 1 {
		cfg.Diversity.SemanticDuplicateThreshold = 0.92
	}
	if cfg.Diversity.SemanticDuplicatePenalty < 0 {
		cfg.Diversity.SemanticDuplicatePenalty = 1
	}
	if cfg.Trace.ResultRetentionDays <= 0 {
		cfg.Trace.ResultRetentionDays = 30
	}
	if cfg.Trace.RequestRetentionDays < cfg.Trace.ResultRetentionDays {
		cfg.Trace.RequestRetentionDays = cfg.Trace.ResultRetentionDays
	}
	if cfg.Trace.RequestRetentionDays < 90 {
		cfg.Trace.RequestRetentionDays = 90
	}
	if cfg.Trace.CleanupIntervalHours <= 0 {
		cfg.Trace.CleanupIntervalHours = 6
	}
	if cfg.Trace.CleanupBatchSize <= 0 {
		cfg.Trace.CleanupBatchSize = 5000
	}
	if cfg.Trace.PersistTimeoutMS <= 0 {
		cfg.Trace.PersistTimeoutMS = defaultTracePersistTimeoutMS
	}
	if cfg.Trace.QueueCapacity <= 0 {
		cfg.Trace.QueueCapacity = defaultTraceQueueCapacity
	}
	if cfg.Trace.WorkerCount <= 0 {
		cfg.Trace.WorkerCount = defaultTraceWorkerCount
	}
	if cfg.Trace.ShutdownDrainTimeoutMS <= 0 {
		cfg.Trace.ShutdownDrainTimeoutMS = defaultTraceShutdownDrainTimeoutMS
	}
	if cfg.SemanticRecall.RecentWindowDays <= 0 {
		cfg.SemanticRecall.RecentWindowDays = 7
	}
	if cfg.SemanticRecall.RecentRatio <= 0 || cfg.SemanticRecall.RecentRatio >= 1 {
		cfg.SemanticRecall.RecentRatio = 0.85
	}
	if cfg.Fusion.RankConstant <= 0 {
		cfg.Fusion.RankConstant = 60
	}
	if cfg.Trending.MaxAgeDays <= 0 {
		cfg.Trending.MaxAgeDays = 3
	}
	if cfg.Trending.HalfLifeHours <= 0 {
		cfg.Trending.HalfLifeHours = 12
	}
	if cfg.Trending.ReplyFactor < 0 {
		cfg.Trending.ReplyFactor = 1.5
	}
	if cfg.TrendingWeight < 0 {
		cfg.TrendingWeight = 0.5
	}
	if math.IsNaN(cfg.LanguageAffinity.Weight) || math.IsInf(cfg.LanguageAffinity.Weight, 0) || cfg.LanguageAffinity.Weight < 0 || cfg.LanguageAffinity.Weight > 1 {
		cfg.LanguageAffinity.Weight = 0.35
	}
	if math.IsNaN(cfg.LanguageAffinity.EvidenceSaturationScale) || math.IsInf(cfg.LanguageAffinity.EvidenceSaturationScale, 0) || cfg.LanguageAffinity.EvidenceSaturationScale <= 0 {
		cfg.LanguageAffinity.EvidenceSaturationScale = 5
	}
	if math.IsNaN(cfg.LanguageAffinity.MaxBehaviorShare) || math.IsInf(cfg.LanguageAffinity.MaxBehaviorShare, 0) || cfg.LanguageAffinity.MaxBehaviorShare < 0 || cfg.LanguageAffinity.MaxBehaviorShare > 1 {
		cfg.LanguageAffinity.MaxBehaviorShare = 0.95
	}
	return cfg
}

func settingProvided(presence map[string]bool, path string, legacyProvided bool) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	return legacyProvided || presence[path]
}
func applyCandidateCaps(target *config.RecommendationCandidateCaps, set config.RecommendationCandidateCaps) {
	if set.Semantic > 0 {
		target.Semantic = set.Semantic
	}
	if set.Following > 0 {
		target.Following = set.Following
	}
	if set.Recent > 0 {
		target.Recent = set.Recent
	}
	if set.Trending > 0 {
		target.Trending = set.Trending
	}
	if set.Merged > 0 {
		target.Merged = set.Merged
	}
}
