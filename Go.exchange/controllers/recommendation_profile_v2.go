package controllers

import (
	"errors"
	"math"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"
)

func defaultRecommendationConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
		BehaviorWeights: config.RecommendationBehaviorWeights{
			View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, Reply: 5, QuickBounce: -3, NotInterested: -6,
		},
		SemanticRecall:     config.RecommendationSemanticRecallConfig{RecentWindowDays: 30, RecentRatio: 0.80},
		Trending:           config.RecommendationTrendingConfig{MaxAgeDays: 7, HalfLifeHours: 24, ReplyFactor: 0.5},
		Exploration:        config.RecommendationExplorationConfig{Ratio: 0.10, MaxSlots: 3, RecentWindowDays: 7, NovelPostMaxAgeDays: 30},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90,
		PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
		SemanticWeight: 4, NegativeSemanticWeight: 1.5, NegativeConfidenceSaturationScale: 12,
		TrendingWeight:       0.5,
		AuthorAffinityWeight: 1, AuthorAffinitySaturationScale: 6, FollowingBonus: 0.5,
		OutOfNetworkMinRatio:       0.30,
		ServedHardExclusionMinutes: 30, ServedSoftLookbackDays: 7, ServedHistoryLimit: 1000,
		Diversity: config.RecommendationDiversityConfig{
			Enabled: true, AuthorWindowSize: 8, MaxSameAuthorInWindow: 2,
			SemanticDuplicateThreshold: 0.92, SemanticDuplicatePenalty: 1,
		},
		Trace: config.RecommendationTraceConfig{ResultRetentionDays: 30, RequestRetentionDays: 90, CleanupIntervalHours: 6, CleanupBatchSize: 5000},
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

func normalizedRecommendationConfig() config.RecommendationConfig {
	cfg := defaultRecommendationConfig()
	if config.AppConfig == nil {
		return cfg
	}
	set := config.AppConfig.Recommendation
	if recommendationSettingProvided("behavior_weights.view", set.BehaviorWeights.View != 0) {
		cfg.BehaviorWeights.View = set.BehaviorWeights.View
	}
	if recommendationSettingProvided("behavior_weights.like", set.BehaviorWeights.Like != 0) {
		cfg.BehaviorWeights.Like = set.BehaviorWeights.Like
	}
	if recommendationSettingProvided("behavior_weights.click", set.BehaviorWeights.Click != 0) {
		cfg.BehaviorWeights.Click = set.BehaviorWeights.Click
	}
	if recommendationSettingProvided("behavior_weights.qualified_read", set.BehaviorWeights.QualifiedRead != 0) {
		cfg.BehaviorWeights.QualifiedRead = set.BehaviorWeights.QualifiedRead
	}
	if set.BehaviorWeights.Reply >= 0 && recommendationSettingProvided("behavior_weights.reply", set.BehaviorWeights.Reply > 0) {
		cfg.BehaviorWeights.Reply = set.BehaviorWeights.Reply
	}
	if recommendationSettingProvided("behavior_weights.quick_bounce", set.BehaviorWeights.QuickBounce != 0) {
		cfg.BehaviorWeights.QuickBounce = set.BehaviorWeights.QuickBounce
	}
	if recommendationSettingProvided("behavior_weights.not_interested", set.BehaviorWeights.NotInterested != 0) {
		cfg.BehaviorWeights.NotInterested = set.BehaviorWeights.NotInterested
	}
	if set.SignalHalfLifeDays > 0 {
		cfg.SignalHalfLifeDays = set.SignalHalfLifeDays
	}
	if set.FeedbackLookbackDays > 0 {
		cfg.FeedbackLookbackDays = set.FeedbackLookbackDays
	}
	if set.PositiveSignalCoexistBonus >= 0 && recommendationSettingProvided("positive_signal_coexist_bonus", set.PositiveSignalCoexistBonus != 0) {
		cfg.PositiveSignalCoexistBonus = set.PositiveSignalCoexistBonus
	}
	if set.PositivePostWeightCap > 0 {
		cfg.PositivePostWeightCap = set.PositivePostWeightCap
	}
	if set.SemanticWeight >= 0 && recommendationSettingProvided("semantic_weight", set.SemanticWeight != 0) {
		cfg.SemanticWeight = set.SemanticWeight
	}
	if set.NegativeSemanticWeight >= 0 && recommendationSettingProvided("negative_semantic_weight", set.NegativeSemanticWeight != 0) {
		cfg.NegativeSemanticWeight = set.NegativeSemanticWeight
	}
	if set.NegativeConfidenceSaturationScale > 0 {
		cfg.NegativeConfidenceSaturationScale = set.NegativeConfidenceSaturationScale
	}
	if recommendationSettingProvided("trending_weight", set.TrendingWeight != 0) {
		cfg.TrendingWeight = set.TrendingWeight
	}
	if recommendationSettingProvided("trending.reply_factor", set.Trending.ReplyFactor != 0) {
		cfg.Trending.ReplyFactor = set.Trending.ReplyFactor
	}
	if set.SemanticRecall.RecentWindowDays > 0 {
		cfg.SemanticRecall.RecentWindowDays = set.SemanticRecall.RecentWindowDays
	}
	if recommendationSettingProvided("semantic_recall.recent_ratio", set.SemanticRecall.RecentRatio != 0) {
		cfg.SemanticRecall.RecentRatio = set.SemanticRecall.RecentRatio
	}
	if set.Trending.MaxAgeDays > 0 {
		cfg.Trending.MaxAgeDays = set.Trending.MaxAgeDays
	}
	if set.Trending.HalfLifeHours > 0 {
		cfg.Trending.HalfLifeHours = set.Trending.HalfLifeHours
	}
	if recommendationSettingProvided("exploration.ratio", set.Exploration.Ratio != 0) {
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
	if set.AuthorAffinityWeight >= 0 && recommendationSettingProvided("author_affinity_weight", set.AuthorAffinityWeight != 0) {
		cfg.AuthorAffinityWeight = set.AuthorAffinityWeight
	}
	if set.AuthorAffinitySaturationScale > 0 {
		cfg.AuthorAffinitySaturationScale = set.AuthorAffinitySaturationScale
	}
	if set.FollowingBonus >= 0 && recommendationSettingProvided("following_bonus", set.FollowingBonus != 0) {
		cfg.FollowingBonus = set.FollowingBonus
	}
	if set.OutOfNetworkMinRatio >= 0 && set.OutOfNetworkMinRatio <= 1 && recommendationSettingProvided("out_of_network_min_ratio", set.OutOfNetworkMinRatio != 0) {
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
	if recommendationSettingProvided("diversity.enabled", set.Diversity.Enabled) {
		cfg.Diversity.Enabled = set.Diversity.Enabled
	}
	if set.Diversity.AuthorWindowSize > 0 {
		cfg.Diversity.AuthorWindowSize = set.Diversity.AuthorWindowSize
	}
	if set.Diversity.MaxSameAuthorInWindow > 0 {
		cfg.Diversity.MaxSameAuthorInWindow = set.Diversity.MaxSameAuthorInWindow
	}
	if set.Diversity.SemanticDuplicateThreshold >= -1 && set.Diversity.SemanticDuplicateThreshold <= 1 && recommendationSettingProvided("diversity.semantic_duplicate_threshold", set.Diversity.SemanticDuplicateThreshold != 0) {
		cfg.Diversity.SemanticDuplicateThreshold = set.Diversity.SemanticDuplicateThreshold
	}
	if set.Diversity.SemanticDuplicatePenalty >= 0 && recommendationSettingProvided("diversity.semantic_duplicate_penalty", set.Diversity.SemanticDuplicatePenalty != 0) {
		cfg.Diversity.SemanticDuplicatePenalty = set.Diversity.SemanticDuplicatePenalty
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
	if cfg.SemanticRecall.RecentWindowDays <= 0 {
		cfg.SemanticRecall.RecentWindowDays = 30
	}
	if cfg.SemanticRecall.RecentRatio <= 0 || cfg.SemanticRecall.RecentRatio >= 1 {
		cfg.SemanticRecall.RecentRatio = 0.80
	}
	if cfg.Trending.MaxAgeDays <= 0 {
		cfg.Trending.MaxAgeDays = 7
	}
	if cfg.Trending.HalfLifeHours <= 0 {
		cfg.Trending.HalfLifeHours = 24
	}
	if cfg.Trending.ReplyFactor < 0 {
		cfg.Trending.ReplyFactor = 0.5
	}
	if cfg.TrendingWeight < 0 {
		cfg.TrendingWeight = 0.5
	}
	return cfg
}

func recommendationSettingProvided(path string, legacyProvided bool) bool {
	return legacyProvided || (config.AppConfig != nil && config.AppConfig.HasRecommendationSetting(path))
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

type userInterestProfile struct {
	PositiveVector                []float32
	NegativeVector                []float32
	NegativeConfidence            float64
	InteractedPostIDs          map[uint]struct{}
	PositiveSignalCount           int
	NegativeSignalCount           int
	PersonalizedSignalCount       int
	PositiveContributions         map[uint]float64
	PositiveAffinityContributions map[uint]float64
	AuthorAffinity                map[uint]float64
	FollowingAuthorIDs            map[uint]struct{}
	ProfileVersion                string
	ProfileConfigHash             string
	ProfileStatus                 string
	ProfileAgeMS                  int64
	MaterializedInteractionsReady bool
}

var loadRecommendationPostEmbeddings = func(postIDs []uint, version string) (map[uint][]float32, error) {
	result := make(map[uint][]float32)
	if len(postIDs) == 0 {
		return result, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []models.PostEmbedding
	if err := global.Db.Select("post_id, embedding").Where("post_id IN ? AND version = ?", postIDs, version).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.PostID] = append([]float32(nil), row.Embedding.Slice()...)
	}
	return result, nil
}

func buildEmbeddingInterestProfile(behaviors []postBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	profile := userInterestProfile{
		InteractedPostIDs: make(map[uint]struct{}), PositiveContributions: make(map[uint]float64), PositiveAffinityContributions: make(map[uint]float64),
	}
	behaviorRows := make([]models.PostBehavior, 0, len(behaviors))
	for _, item := range behaviors {
		behaviorRows = append(behaviorRows, item.Behavior)
	}
	feedbackRows := make([]recommendation.FeedbackEvent, 0, len(feedback))
	for _, item := range feedback {
		feedbackRows = append(feedbackRows, recommendation.FeedbackEvent{
			EventID: item.Event.EventID, PostID: item.Event.PostID, EventType: item.Event.EventType,
			OccurredAt: item.Event.OccurredAt, ReceivedAt: item.Event.ReceivedAt, ReadOutcome: item.Event.ReadOutcome,
		})
	}
	reactionRows := make(map[uint]recommendation.ReactionState, len(reactions))
	for postID, reaction := range reactions {
		reactionRows[postID] = recommendation.ReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
	}
	canonical := recommendation.CanonicalizeOutcomes(behaviorRows, feedbackRows, reactionRows)
	built, err := recommendation.BuildInterestProfile(canonical, now, cfg, config.ActiveEmbeddingVersion(), func(ids []uint, version string) (map[uint][]float32, error) {
		return loadRecommendationPostEmbeddings(ids, version)
	})
	if err != nil {
		return profile, err
	}
	for _, postID := range built.InteractedPostIDs {
		profile.InteractedPostIDs[postID] = struct{}{}
	}
	profile.PositiveVector = built.PositiveVector
	profile.NegativeVector = built.NegativeVector
	profile.PositiveSignalCount = built.PositiveSignalCount
	profile.NegativeSignalCount = built.NegativeSignalCount
	profile.PersonalizedSignalCount = built.PersonalizedSignalCount
	profile.PositiveContributions = built.PositiveContributions
	profile.PositiveAffinityContributions = built.PositiveAffinityContributions
	if len(profile.NegativeVector) > 0 && cfg.NegativeConfidenceSaturationScale > 0 {
		profile.NegativeConfidence = math.Tanh(built.NegativeEvidence / cfg.NegativeConfidenceSaturationScale)
	}
	return profile, nil
}

func addEmbeddingContribution(target *[]float32, vector []float32, strength float64) bool {
	return recommendation.AddEmbeddingContribution(target, vector, strength)
}

func recommendationPositivePostStrength(outcome userPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	return recommendation.PositivePostStrength(sharedUserPostOutcome(outcome), now, cfg)
}

func recommendationAuthorAffinityContribution(outcome userPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	return recommendation.AuthorAffinityContribution(sharedUserPostOutcome(outcome), now, cfg)
}
func recommendationNegativePostStrength(outcome userPostOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	return recommendation.NegativePostStrength(sharedUserPostOutcome(outcome), now, cfg)
}

func validEmbeddingVector(vector []float32) bool {
	return recommendation.ValidEmbeddingVector(vector)
}

func normalizeEmbedding(vector []float32) []float32 {
	return recommendation.NormalizeEmbedding(vector)
}

func embeddingSignalWeight(cfg config.RecommendationConfig, signal string) float64 {
	return recommendation.EmbeddingSignalWeight(cfg, signal)
}

func recommendationSignalDecay(now, occurred time.Time, halfLifeDays float64) float64 {
	return recommendation.SignalDecay(now, occurred, halfLifeDays)
}

func cosineSimilarity(left, right []float32) float64 {
	return recommendation.CosineSimilarity(left, right)
}

func sharedUserPostOutcome(outcome userPostOutcome) recommendation.UserPostOutcome {
	converted := recommendation.UserPostOutcome{PostID: outcome.PostID}
	for _, signal := range outcome.PositiveSignals {
		converted.PositiveSignals = append(converted.PositiveSignals, recommendation.UserPostSignal{SignalType: signal.SignalType, OccurredAt: signal.OccurredAt})
	}
	if outcome.NegativeSignal != nil {
		converted.NegativeSignal = &recommendation.UserPostSignal{SignalType: outcome.NegativeSignal.SignalType, OccurredAt: outcome.NegativeSignal.OccurredAt}
	}
	if outcome.PassiveSignal != nil {
		converted.PassiveSignal = &recommendation.UserPostSignal{SignalType: outcome.PassiveSignal.SignalType, OccurredAt: outcome.PassiveSignal.OccurredAt}
	}
	return converted
}

func clampRecommendationSimilarity(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}
