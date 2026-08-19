package controllers

import (
	"testing"

	"Go.exchange/config"
)

func TestNormalizedRecommendationConfigMissingFieldsPreserveDefaults(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = &config.Config{}
	t.Cleanup(func() { config.AppConfig = original })

	cfg := normalizedRecommendationConfig()
	if cfg.BehaviorWeights.Reply != 5 || cfg.FollowingBonus != 0.5 ||
		cfg.OutOfNetworkMinRatio != 0.30 || cfg.NovelAuthorMinRatio != 0.10 ||
		!cfg.Diversity.Enabled || cfg.Diversity.SemanticDuplicatePenalty != 1 {
		t.Fatalf("normalized config=%#v", cfg)
	}
}

func TestNormalizedRecommendationConfigProgrammaticOverridesRemainSupported(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = &config.Config{Recommendation: config.RecommendationConfig{
		FollowingBonus:       0.25,
		OutOfNetworkMinRatio: 0.40,
	}}
	t.Cleanup(func() { config.AppConfig = original })

	cfg := normalizedRecommendationConfig()
	if cfg.FollowingBonus != 0.25 || cfg.OutOfNetworkMinRatio != 0.40 {
		t.Fatalf("normalized config=%#v", cfg)
	}
}

func TestNormalizedRecommendationConfigExplicitZeroOverrides(t *testing.T) {
	tests := []struct {
		name string
		path string
		set  func(*config.RecommendationConfig)
		get  func(config.RecommendationConfig) float64
	}{
		{name: "behavior_weights.view", path: "behavior_weights.view", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.View = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.View }},
		{name: "behavior_weights.like", path: "behavior_weights.like", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.Like = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.Like }},
		{name: "behavior_weights.click", path: "behavior_weights.click", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.Click = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.Click }},
		{name: "behavior_weights.qualified_read", path: "behavior_weights.qualified_read", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.QualifiedRead = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.QualifiedRead }},
		{name: "behavior_weights.reply", path: "behavior_weights.reply", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.Reply = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.Reply }},
		{name: "behavior_weights.quick_bounce", path: "behavior_weights.quick_bounce", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.QuickBounce = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.QuickBounce }},
		{name: "behavior_weights.not_interested", path: "behavior_weights.not_interested", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.NotInterested = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.NotInterested }},
		{name: "positive_signal_coexist_bonus", path: "positive_signal_coexist_bonus", set: func(cfg *config.RecommendationConfig) { cfg.PositiveSignalCoexistBonus = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.PositiveSignalCoexistBonus }},
		{name: "semantic_weight", path: "semantic_weight", set: func(cfg *config.RecommendationConfig) { cfg.SemanticWeight = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.SemanticWeight }},
		{name: "negative_semantic_weight", path: "negative_semantic_weight", set: func(cfg *config.RecommendationConfig) { cfg.NegativeSemanticWeight = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.NegativeSemanticWeight }},
		{name: "freshness_weight", path: "freshness_weight", set: func(cfg *config.RecommendationConfig) { cfg.FreshnessWeight = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.FreshnessWeight }},
		{name: "trending_weight", path: "trending_weight", set: func(cfg *config.RecommendationConfig) { cfg.TrendingWeight = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.TrendingWeight }},
		{name: "trending.comment_factor", path: "trending.comment_factor", set: func(cfg *config.RecommendationConfig) { cfg.Trending.CommentFactor = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Trending.CommentFactor }},
		{name: "author_affinity_weight", path: "author_affinity_weight", set: func(cfg *config.RecommendationConfig) { cfg.AuthorAffinityWeight = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.AuthorAffinityWeight }},
		{name: "following_bonus", path: "following_bonus", set: func(cfg *config.RecommendationConfig) { cfg.FollowingBonus = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.FollowingBonus }},
		{name: "diversity.semantic_duplicate_threshold", path: "diversity.semantic_duplicate_threshold", set: func(cfg *config.RecommendationConfig) { cfg.Diversity.SemanticDuplicateThreshold = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Diversity.SemanticDuplicateThreshold }},
		{name: "diversity.semantic_duplicate_penalty", path: "diversity.semantic_duplicate_penalty", set: func(cfg *config.RecommendationConfig) { cfg.Diversity.SemanticDuplicatePenalty = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Diversity.SemanticDuplicatePenalty }},
	}

	original := config.AppConfig
	t.Cleanup(func() { config.AppConfig = original })
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set := config.RecommendationConfig{}
			tc.set(&set)
			config.AppConfig = &config.Config{
				Recommendation:         set,
				RecommendationPresence: map[string]bool{tc.path: true},
			}
			if got := tc.get(normalizedRecommendationConfig()); got != 0 {
				t.Fatalf("normalized %s=%v, want 0", tc.path, got)
			}
		})
	}
}

func TestNormalizedRecommendationConfigTrendingAndSemanticRecallValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
		set  func(*config.RecommendationConfig)
		get  func(config.RecommendationConfig) float64
		want float64
	}{
		{name: "recent window zero", path: "semantic_recall.recent_window_days", set: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentWindowDays = 0 }, get: func(cfg config.RecommendationConfig) float64 { return float64(cfg.SemanticRecall.RecentWindowDays) }, want: 30},
		{name: "recent ratio zero", path: "semantic_recall.recent_ratio", set: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentRatio = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.SemanticRecall.RecentRatio }, want: 0.80},
		{name: "recent ratio one", path: "semantic_recall.recent_ratio", set: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentRatio = 1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.SemanticRecall.RecentRatio }, want: 0.80},
		{name: "max age zero", path: "trending.max_age_days", set: func(cfg *config.RecommendationConfig) { cfg.Trending.MaxAgeDays = 0 }, get: func(cfg config.RecommendationConfig) float64 { return float64(cfg.Trending.MaxAgeDays) }, want: 7},
		{name: "half life zero", path: "trending.half_life_hours", set: func(cfg *config.RecommendationConfig) { cfg.Trending.HalfLifeHours = 0 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Trending.HalfLifeHours }, want: 24},
		{name: "comment factor negative", path: "trending.comment_factor", set: func(cfg *config.RecommendationConfig) { cfg.Trending.CommentFactor = -1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Trending.CommentFactor }, want: 0.5},
		{name: "trending weight negative", path: "trending_weight", set: func(cfg *config.RecommendationConfig) { cfg.TrendingWeight = -1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.TrendingWeight }, want: 0.5},
	}

	original := config.AppConfig
	t.Cleanup(func() { config.AppConfig = original })
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set := config.RecommendationConfig{}
			tc.set(&set)
			config.AppConfig = &config.Config{
				Recommendation:         set,
				RecommendationPresence: map[string]bool{tc.path: true},
			}
			if got := tc.get(normalizedRecommendationConfig()); got != tc.want {
				t.Fatalf("normalized %s=%v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestNormalizedRecommendationConfigExplicitZeroRatios(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = &config.Config{RecommendationPresence: map[string]bool{
		"out_of_network_min_ratio": true,
		"novel_author_min_ratio":   true,
	}}
	t.Cleanup(func() { config.AppConfig = original })

	cfg := normalizedRecommendationConfig()
	if cfg.OutOfNetworkMinRatio != 0 || cfg.NovelAuthorMinRatio != 0 {
		t.Fatalf("normalized ratios=%#v, want both zero", cfg)
	}
}

func TestNormalizedRecommendationConfigExplicitFalseDisablesDiversity(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = &config.Config{
		Recommendation:         config.RecommendationConfig{Diversity: config.RecommendationDiversityConfig{Enabled: false}},
		RecommendationPresence: map[string]bool{"diversity.enabled": true},
	}
	t.Cleanup(func() { config.AppConfig = original })

	if cfg := normalizedRecommendationConfig(); cfg.Diversity.Enabled {
		t.Fatal("explicit diversity.enabled=false must disable diversity")
	}
}

func TestNormalizedRecommendationConfigInvalidValuesRemainInvalid(t *testing.T) {
	tests := []struct {
		name string
		path string
		set  func(*config.RecommendationConfig)
		get  func(config.RecommendationConfig) float64
		want float64
	}{
		{name: "reply negative", path: "behavior_weights.reply", set: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.Reply = -1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.BehaviorWeights.Reply }, want: 5},
		{name: "out ratio negative", path: "out_of_network_min_ratio", set: func(cfg *config.RecommendationConfig) { cfg.OutOfNetworkMinRatio = -0.1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.OutOfNetworkMinRatio }, want: 0.30},
		{name: "out ratio above one", path: "out_of_network_min_ratio", set: func(cfg *config.RecommendationConfig) { cfg.OutOfNetworkMinRatio = 1.1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.OutOfNetworkMinRatio }, want: 0.30},
		{name: "novel ratio above one", path: "novel_author_min_ratio", set: func(cfg *config.RecommendationConfig) { cfg.NovelAuthorMinRatio = 2 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.NovelAuthorMinRatio }, want: 0.10},
		{name: "semantic threshold above one", path: "diversity.semantic_duplicate_threshold", set: func(cfg *config.RecommendationConfig) { cfg.Diversity.SemanticDuplicateThreshold = 2 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Diversity.SemanticDuplicateThreshold }, want: 0.92},
		{name: "semantic penalty negative", path: "diversity.semantic_duplicate_penalty", set: func(cfg *config.RecommendationConfig) { cfg.Diversity.SemanticDuplicatePenalty = -1 }, get: func(cfg config.RecommendationConfig) float64 { return cfg.Diversity.SemanticDuplicatePenalty }, want: 1},
	}

	original := config.AppConfig
	t.Cleanup(func() { config.AppConfig = original })
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set := config.RecommendationConfig{}
			tc.set(&set)
			config.AppConfig = &config.Config{
				Recommendation:         set,
				RecommendationPresence: map[string]bool{tc.path: true},
			}
			if got := tc.get(normalizedRecommendationConfig()); got != tc.want {
				t.Fatalf("normalized %s=%v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestNormalizedRecommendationConfigExplicitZeroDoesNotRelaxPositiveOnlyFields(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = &config.Config{Recommendation: config.RecommendationConfig{
		SignalHalfLifeDays:                0,
		PositiveArticleWeightCap:          0,
		NegativeConfidenceSaturationScale: 0,
		ServedHistoryLimit:                0,
		Diversity:                         config.RecommendationDiversityConfig{AuthorWindowSize: 0},
		Trace:                             config.RecommendationTraceConfig{CleanupBatchSize: 0},
	}}
	t.Cleanup(func() { config.AppConfig = original })

	cfg := normalizedRecommendationConfig()
	if cfg.SignalHalfLifeDays != 14 || cfg.PositiveArticleWeightCap != 7 ||
		cfg.NegativeConfidenceSaturationScale != 12 || cfg.ServedHistoryLimit != 1000 ||
		cfg.Diversity.AuthorWindowSize != 8 || cfg.Trace.CleanupBatchSize != 5000 {
		t.Fatalf("positive-only fields changed by explicit zero: %#v", cfg)
	}
}
