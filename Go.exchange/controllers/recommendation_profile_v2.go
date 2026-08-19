package controllers

import (
	"errors"
	"math"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
)

func defaultRecommendationConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
		BehaviorWeights: config.RecommendationBehaviorWeights{
			View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, Reply: 5, QuickBounce: -3, NotInterested: -6,
		},
		SemanticRecall:     config.RecommendationSemanticRecallConfig{RecentWindowDays: 30, RecentRatio: 0.80},
		Trending:           config.RecommendationTrendingConfig{MaxAgeDays: 7, HalfLifeHours: 24, CommentFactor: 0.5},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90,
		PositiveSignalCoexistBonus: 1, PositiveArticleWeightCap: 7,
		SemanticWeight: 4, NegativeSemanticWeight: 1.5, NegativeConfidenceSaturationScale: 12,
		FreshnessWeight: 2, FreshnessHalfLifeDays: 2, TrendingWeight: 0.5,
		AuthorAffinityWeight: 1, AuthorAffinitySaturationScale: 6, FollowingBonus: 0.5,
		OutOfNetworkMinRatio: 0.30, NovelAuthorMinRatio: 0.10,
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
	if set.PositiveArticleWeightCap > 0 {
		cfg.PositiveArticleWeightCap = set.PositiveArticleWeightCap
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
	if set.FreshnessWeight >= 0 && recommendationSettingProvided("freshness_weight", set.FreshnessWeight != 0) {
		cfg.FreshnessWeight = set.FreshnessWeight
	}
	if set.FreshnessHalfLifeDays > 0 {
		cfg.FreshnessHalfLifeDays = set.FreshnessHalfLifeDays
	}
	if recommendationSettingProvided("trending_weight", set.TrendingWeight != 0) {
		cfg.TrendingWeight = set.TrendingWeight
	}
	if recommendationSettingProvided("trending.comment_factor", set.Trending.CommentFactor != 0) {
		cfg.Trending.CommentFactor = set.Trending.CommentFactor
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
	if set.NovelAuthorMinRatio >= 0 && set.NovelAuthorMinRatio <= 1 && recommendationSettingProvided("novel_author_min_ratio", set.NovelAuthorMinRatio != 0) {
		cfg.NovelAuthorMinRatio = set.NovelAuthorMinRatio
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

	if cfg.BehaviorWeights.Reply < 0 {
		cfg.BehaviorWeights.Reply = 5
	}
	if cfg.PositiveSignalCoexistBonus < 0 {
		cfg.PositiveSignalCoexistBonus = 1
	}
	if cfg.PositiveArticleWeightCap <= 0 || cfg.PositiveArticleWeightCap < math.Max(cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Reply) {
		cfg.PositiveArticleWeightCap = 7
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
	if cfg.NovelAuthorMinRatio < 0 || cfg.NovelAuthorMinRatio > 1 {
		cfg.NovelAuthorMinRatio = 0.10
	}
	if cfg.NovelAuthorMinRatio > cfg.OutOfNetworkMinRatio {
		cfg.NovelAuthorMinRatio = math.Min(0.10, cfg.OutOfNetworkMinRatio)
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
	if cfg.Trending.CommentFactor < 0 {
		cfg.Trending.CommentFactor = 0.5
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
	InteractedArticleIDs          map[uint]struct{}
	PositiveSignalCount           int
	NegativeSignalCount           int
	PersonalizedSignalCount       int
	PositiveContributions         map[uint]float64
	PositiveAffinityContributions map[uint]float64
	AuthorAffinity                map[uint]float64
	FollowingAuthorIDs            map[uint]struct{}
}

var loadRecommendationArticleEmbeddings = func(articleIDs []uint, version string) (map[uint][]float32, error) {
	result := make(map[uint][]float32)
	if len(articleIDs) == 0 {
		return result, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []models.ArticleEmbedding
	if err := global.Db.Select("article_id, embedding").Where("article_id IN ? AND version = ?", articleIDs, version).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ArticleID] = append([]float32(nil), row.Embedding.Slice()...)
	}
	return result, nil
}

func buildEmbeddingInterestProfile(behaviors []articleBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	if cfg.PositiveArticleWeightCap <= 0 {
		cfg.PositiveArticleWeightCap = 7
	}
	if cfg.NegativeConfidenceSaturationScale <= 0 {
		cfg.NegativeConfidenceSaturationScale = 12
	}
	if cfg.SignalHalfLifeDays <= 0 {
		cfg.SignalHalfLifeDays = 14
	}

	profile := userInterestProfile{
		InteractedArticleIDs: make(map[uint]struct{}), PositiveContributions: make(map[uint]float64), PositiveAffinityContributions: make(map[uint]float64),
	}
	for articleID := range reactions {
		if articleID != 0 {
			profile.InteractedArticleIDs[articleID] = struct{}{}
		}
	}
	outcomes := canonicalizeRecommendationOutcomes(behaviors, feedback, reactions)
	ids := make([]uint, 0, len(outcomes))
	for _, outcome := range outcomes {
		profile.InteractedArticleIDs[outcome.ArticleID] = struct{}{}
		ids = append(ids, outcome.ArticleID)
	}
	embeddingsByArticle, err := loadRecommendationArticleEmbeddings(ids, config.ActiveEmbeddingVersion())
	if err != nil {
		return profile, err
	}
	negativeEvidence := 0.0
	for _, outcome := range outcomes {
		positiveStrength := recommendationPositiveArticleStrength(outcome, now, cfg)
		if positiveStrength > 0 {
			profile.PositiveContributions[outcome.ArticleID] = positiveStrength
			if affinityStrength := recommendationAuthorAffinityContribution(outcome, now, cfg); affinityStrength > 0 {
				profile.PositiveAffinityContributions[outcome.ArticleID] = affinityStrength
			}
		}
		vector := embeddingsByArticle[outcome.ArticleID]
		if !validEmbeddingVector(vector) {
			continue
		}
		if positiveStrength > 0 && addEmbeddingContribution(&profile.PositiveVector, vector, positiveStrength) {
			profile.PositiveSignalCount++
		}
		negativeStrength := recommendationNegativeArticleStrength(outcome, now, cfg)
		if negativeStrength > 0 && addEmbeddingContribution(&profile.NegativeVector, vector, negativeStrength) {
			negativeEvidence += negativeStrength
			profile.NegativeSignalCount++
		}
	}
	profile.PositiveVector = normalizeEmbedding(profile.PositiveVector)
	profile.NegativeVector = normalizeEmbedding(profile.NegativeVector)
	if len(profile.NegativeVector) > 0 && cfg.NegativeConfidenceSaturationScale > 0 {
		profile.NegativeConfidence = math.Tanh(negativeEvidence / cfg.NegativeConfidenceSaturationScale)
	}
	profile.PersonalizedSignalCount = profile.PositiveSignalCount + profile.NegativeSignalCount
	return profile, nil
}

func addEmbeddingContribution(target *[]float32, vector []float32, strength float64) bool {
	if strength <= 0 || !validEmbeddingVector(vector) {
		return false
	}
	if len(*target) == 0 {
		*target = make([]float32, len(vector))
	}
	if len(*target) != len(vector) {
		return false
	}
	for index, value := range vector {
		(*target)[index] += float32(float64(value) * strength)
	}
	return true
}

func recommendationPositiveArticleStrength(outcome userArticleOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if len(outcome.PositiveSignals) > 0 {
		decayed := make([]float64, len(outcome.PositiveSignals))
		primaryIndex, primary := 0, 0.0
		for index, signal := range outcome.PositiveSignals {
			decayed[index] = embeddingSignalWeight(cfg, signal.SignalType) * recommendationSignalDecay(now, signal.OccurredAt, cfg.SignalHalfLifeDays)
			if decayed[index] > primary {
				primaryIndex, primary = index, decayed[index]
			}
		}
		coexist := 0.0
		for index, signal := range outcome.PositiveSignals {
			if index != primaryIndex {
				coexist += cfg.PositiveSignalCoexistBonus * recommendationSignalDecay(now, signal.OccurredAt, cfg.SignalHalfLifeDays)
			}
		}
		return math.Min(cfg.PositiveArticleWeightCap, primary+coexist)
	}
	if outcome.PassiveSignal == nil {
		return 0
	}
	if outcome.PassiveSignal.SignalType == "quick_bounce" || outcome.PassiveSignal.SignalType == "neutral_read" || outcome.PassiveSignal.SignalType == "not_interested" {
		return 0
	}
	return math.Max(0, embeddingSignalWeight(cfg, outcome.PassiveSignal.SignalType)*recommendationSignalDecay(now, outcome.PassiveSignal.OccurredAt, cfg.SignalHalfLifeDays))
}

func recommendationAuthorAffinityContribution(outcome userArticleOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if len(outcome.PositiveSignals) > 0 {
		return recommendationPositiveArticleStrength(outcome, now, cfg)
	}
	if outcome.PassiveSignal == nil {
		return 0
	}
	if outcome.PassiveSignal.SignalType != "click" && outcome.PassiveSignal.SignalType != "qualified_read" {
		return 0
	}
	return math.Max(0, embeddingSignalWeight(cfg, outcome.PassiveSignal.SignalType)*recommendationSignalDecay(now, outcome.PassiveSignal.OccurredAt, cfg.SignalHalfLifeDays))
}
func recommendationNegativeArticleStrength(outcome userArticleOutcome, now time.Time, cfg config.RecommendationConfig) float64 {
	if outcome.NegativeSignal == nil {
		return 0
	}
	if outcome.NegativeSignal.SignalType != "quick_bounce" && outcome.NegativeSignal.SignalType != "not_interested" {
		return 0
	}
	return math.Abs(embeddingSignalWeight(cfg, outcome.NegativeSignal.SignalType)) * recommendationSignalDecay(now, outcome.NegativeSignal.OccurredAt, cfg.SignalHalfLifeDays)
}

func validEmbeddingVector(vector []float32) bool {
	if len(vector) == 0 {
		return false
	}
	hasNonZero := false
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		if value != 0 {
			hasNonZero = true
		}
	}
	return hasNonZero
}

func normalizeEmbedding(vector []float32) []float32 {
	if !validEmbeddingVector(vector) {
		return nil
	}
	norm := 0.0
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 {
		return nil
	}
	length := float32(math.Sqrt(norm))
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = value / length
	}
	return result
}

func embeddingSignalWeight(cfg config.RecommendationConfig, signal string) float64 {
	switch signal {
	case "view":
		return cfg.BehaviorWeights.View
	case "click":
		return cfg.BehaviorWeights.Click
	case "qualified_read":
		return cfg.BehaviorWeights.QualifiedRead
	case "reply":
		return cfg.BehaviorWeights.Reply
	case "quick_bounce":
		return cfg.BehaviorWeights.QuickBounce
	case "like":
		return cfg.BehaviorWeights.Like
	case "not_interested":
		return cfg.BehaviorWeights.NotInterested
	default:
		return 0
	}
}

func recommendationSignalDecay(now, occurred time.Time, halfLifeDays float64) float64 {
	if occurred.IsZero() || occurred.After(now) || halfLifeDays <= 0 {
		return 1
	}
	age := now.Sub(occurred).Hours() / 24
	return math.Exp(-math.Ln2 * age / halfLifeDays)
}

func cosineSimilarity(left, right []float32) float64 {
	if !validEmbeddingVector(left) || !validEmbeddingVector(right) || len(left) != len(right) {
		return 0
	}
	dot, leftNorm, rightNorm := 0.0, 0.0, 0.0
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
		leftNorm += float64(left[index]) * float64(left[index])
		rightNorm += float64(right[index]) * float64(right[index])
	}
	if leftNorm <= 0 || rightNorm <= 0 {
		return 0
	}
	value := dot / math.Sqrt(leftNorm*rightNorm)
	return clampRecommendationSimilarity(value)
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
