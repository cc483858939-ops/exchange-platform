package config

import (
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type EmbeddingConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	BaseURL        string `mapstructure:"base_url"`
	APIKey         string `mapstructure:"api_key"`
	Model          string `mapstructure:"model"`
	Version        string `mapstructure:"version"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

const DefaultEmbeddingVersion = "post_embedding_v1"

type KafkaConfig struct {
	Brokers                        []string `mapstructure:"brokers"`
	UserBehaviorTopic              string   `mapstructure:"user_behavior_topic"`
	LikeSnapshotTopic              string   `mapstructure:"like_snapshot_topic"`
	RecommendationEventsTopic      string   `mapstructure:"recommendation_events_topic"`
	ArticleEmbeddingTopic          string   `mapstructure:"article_embedding_topic"`
	ActivityEventsTopic            string   `mapstructure:"activity_events_topic"`
	NotificationDLQTopic           string   `mapstructure:"notification_dlq_topic"`
	TopicReplicationFactor         int      `mapstructure:"topic_replication_factor"`
	UserBehaviorPartitions         int      `mapstructure:"user_behavior_partitions"`
	LikeSnapshotPartitions         int      `mapstructure:"like_snapshot_partitions"`
	RecommendationEventsPartitions int      `mapstructure:"recommendation_events_partitions"`
	ArticleEmbeddingPartitions     int      `mapstructure:"article_embedding_partitions"`
	ActivityEventsPartitions       int      `mapstructure:"activity_events_partitions"`
	NotificationDLQPartitions      int      `mapstructure:"notification_dlq_partitions"`
	UserBehaviorGroupID            string   `mapstructure:"user_behavior_group_id"`
	LikeSnapshotGroupID            string   `mapstructure:"like_snapshot_group_id"`
	RecommendationMetricsGroupID   string   `mapstructure:"recommendation_metrics_group_id"`
	ArticleEmbeddingGroupID        string   `mapstructure:"article_embedding_group_id"`
	NotificationGroupID            string   `mapstructure:"notification_group_id"`
}

type RecommendationBehaviorWeights struct {
	View          float64 `mapstructure:"view"`
	Like          float64 `mapstructure:"like"`
	Click         float64 `mapstructure:"click"`
	QualifiedRead float64 `mapstructure:"qualified_read"`
	Reply         float64 `mapstructure:"reply"`
	QuickBounce   float64 `mapstructure:"quick_bounce"`
	NotInterested float64 `mapstructure:"not_interested"`
}

type RecommendationDiversityConfig struct {
	Enabled                    bool    `mapstructure:"enabled"`
	AuthorWindowSize           int     `mapstructure:"author_window_size"`
	MaxSameAuthorInWindow      int     `mapstructure:"max_same_author_in_window"`
	SemanticDuplicateThreshold float64 `mapstructure:"semantic_duplicate_threshold"`
	SemanticDuplicatePenalty   float64 `mapstructure:"semantic_duplicate_penalty"`
}

type RecommendationSemanticRecallConfig struct {
	RecentWindowDays int     `mapstructure:"recent_window_days"`
	RecentRatio      float64 `mapstructure:"recent_ratio"`
}

type RecommendationTrendingConfig struct {
	MaxAgeDays    int     `mapstructure:"max_age_days"`
	HalfLifeHours float64 `mapstructure:"half_life_hours"`
	CommentFactor float64 `mapstructure:"comment_factor"`
}

type RecommendationExplorationConfig struct {
	Ratio                  float64 `mapstructure:"ratio"`
	MaxSlots               int     `mapstructure:"max_slots"`
	RecentWindowDays       int     `mapstructure:"recent_window_days"`
	NovelArticleMaxAgeDays int     `mapstructure:"novel_article_max_age_days"`
}

type RecommendationTraceConfig struct {
	ResultRetentionDays  int `mapstructure:"result_retention_days"`
	RequestRetentionDays int `mapstructure:"request_retention_days"`
	CleanupIntervalHours int `mapstructure:"cleanup_interval_hours"`
	CleanupBatchSize     int `mapstructure:"cleanup_batch_size"`
}

type RecommendationCandidateCaps struct {
	Semantic  int `mapstructure:"semantic"`
	Following int `mapstructure:"following"`
	Recent    int `mapstructure:"recent"`
	Trending  int `mapstructure:"trending"`
	Merged    int `mapstructure:"merged"`
}

type RecommendationCandidatesConfig struct {
	Personalized RecommendationCandidateCaps `mapstructure:"personalized"`
	ColdStart    RecommendationCandidateCaps `mapstructure:"cold_start"`
}

// RecommendationProfileMaterializationConfig controls the nearline profile
// projection worker. These values affect scheduling only; they are
// deliberately excluded from the profile and ranker configuration hashes.
type RecommendationProfileMaterializationConfig struct {
	DebounceSeconds          int `mapstructure:"debounce_seconds"`
	PollIntervalSeconds      int `mapstructure:"poll_interval_seconds"`
	BatchSize                int `mapstructure:"batch_size"`
	RebuildIntervalHours     int `mapstructure:"rebuild_interval_hours"`
	StaleScanIntervalSeconds int `mapstructure:"stale_scan_interval_seconds"`
	StaleEnqueueBatchSize    int `mapstructure:"stale_enqueue_batch_size"`
}

const (
	DefaultRecommendationProfileDebounceSeconds          = 2
	DefaultRecommendationProfilePollIntervalSeconds      = 1
	DefaultRecommendationProfileBatchSize                = 50
	DefaultRecommendationProfileRebuildIntervalHours     = 6
	DefaultRecommendationProfileStaleScanIntervalSeconds = 60
	DefaultRecommendationProfileStaleEnqueueBatchSize    = 500
)

func (c RecommendationProfileMaterializationConfig) Normalized() RecommendationProfileMaterializationConfig {
	if c.DebounceSeconds <= 0 {
		c.DebounceSeconds = DefaultRecommendationProfileDebounceSeconds
	}
	if c.PollIntervalSeconds <= 0 {
		c.PollIntervalSeconds = DefaultRecommendationProfilePollIntervalSeconds
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultRecommendationProfileBatchSize
	}
	if c.RebuildIntervalHours <= 0 {
		c.RebuildIntervalHours = DefaultRecommendationProfileRebuildIntervalHours
	}
	if c.StaleScanIntervalSeconds <= 0 {
		c.StaleScanIntervalSeconds = DefaultRecommendationProfileStaleScanIntervalSeconds
	}
	if c.StaleEnqueueBatchSize <= 0 {
		c.StaleEnqueueBatchSize = DefaultRecommendationProfileStaleEnqueueBatchSize
	}
	return c
}

type RecommendationConfig struct {
	BehaviorWeights                   RecommendationBehaviorWeights              `mapstructure:"behavior_weights"`
	SemanticRecall                    RecommendationSemanticRecallConfig         `mapstructure:"semantic_recall"`
	Trending                          RecommendationTrendingConfig               `mapstructure:"trending"`
	Exploration                       RecommendationExplorationConfig            `mapstructure:"exploration"`
	SignalHalfLifeDays                float64                                    `mapstructure:"signal_half_life_days"`
	FeedbackLookbackDays              int                                        `mapstructure:"feedback_lookback_days"`
	PositiveSignalCoexistBonus        float64                                    `mapstructure:"positive_signal_coexist_bonus"`
	PositiveArticleWeightCap          float64                                    `mapstructure:"positive_article_weight_cap"`
	SemanticWeight                    float64                                    `mapstructure:"semantic_weight"`
	NegativeSemanticWeight            float64                                    `mapstructure:"negative_semantic_weight"`
	NegativeConfidenceSaturationScale float64                                    `mapstructure:"negative_confidence_saturation_scale"`
	TrendingWeight                    float64                                    `mapstructure:"trending_weight"`
	AuthorAffinityWeight              float64                                    `mapstructure:"author_affinity_weight"`
	AuthorAffinitySaturationScale     float64                                    `mapstructure:"author_affinity_saturation_scale"`
	FollowingBonus                    float64                                    `mapstructure:"following_bonus"`
	OutOfNetworkMinRatio              float64                                    `mapstructure:"out_of_network_min_ratio"`
	ServedHardExclusionMinutes        int                                        `mapstructure:"served_hard_exclusion_minutes"`
	ServedSoftLookbackDays            int                                        `mapstructure:"served_soft_lookback_days"`
	ServedHistoryLimit                int                                        `mapstructure:"served_history_limit"`
	Diversity                         RecommendationDiversityConfig              `mapstructure:"diversity"`
	Trace                             RecommendationTraceConfig                  `mapstructure:"trace"`
	Candidates                        RecommendationCandidatesConfig             `mapstructure:"candidates"`
	ProfileMaterialization            RecommendationProfileMaterializationConfig `mapstructure:"profile_materialization"`
}

type OutboxConfig struct {
	RetentionHours         int `mapstructure:"retention_hours"`
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
	CleanupBatchSize       int `mapstructure:"cleanup_batch_size"`
}

type StorageConfig struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

type Config struct {
	App struct {
		Name string
		Port string
	}
	Database struct {
		Dsn          string
		MaxIdleconns int
		MaxOpenConns int
	}
	Embedding              EmbeddingConfig
	Kafka                  KafkaConfig
	Recommendation         RecommendationConfig
	RecommendationPresence map[string]bool `mapstructure:"-" json:"-" yaml:"-"`
	Outbox                 OutboxConfig
	Storage                StorageConfig
}

var AppConfig *Config

var recommendationPresenceKeys = []string{
	"behavior_weights.view",
	"behavior_weights.like",
	"behavior_weights.click",
	"behavior_weights.qualified_read",
	"behavior_weights.reply",
	"behavior_weights.quick_bounce",
	"behavior_weights.not_interested",
	"semantic_recall.recent_window_days",
	"semantic_recall.recent_ratio",
	"trending.max_age_days",
	"trending.half_life_hours",
	"trending.comment_factor",
	"exploration.ratio",

	"positive_signal_coexist_bonus",

	"semantic_weight",
	"negative_semantic_weight",
	"trending_weight",

	"author_affinity_weight",
	"following_bonus",

	"out_of_network_min_ratio",

	"diversity.enabled",
	"diversity.semantic_duplicate_threshold",
	"diversity.semantic_duplicate_penalty",
}

func recommendationSettingPresence(v *viper.Viper) map[string]bool {
	result := make(map[string]bool)
	if v == nil {
		return result
	}
	for _, relativePath := range recommendationPresenceKeys {
		if v.InConfig("recommendation." + relativePath) {
			result[relativePath] = true
		}
	}
	return result
}

func (c *Config) HasRecommendationSetting(path string) bool {
	if c == nil {
		return false
	}
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	return c.RecommendationPresence[path]
}

// ActiveEmbeddingVersion returns the one embedding-space identity used by
// workers, profile construction, and semantic recall.
func ActiveEmbeddingVersion() string {
	if AppConfig != nil {
		if version := strings.TrimSpace(AppConfig.Embedding.Version); version != "" {
			return version
		}
	}
	return DefaultEmbeddingVersion
}

func InitConfig() {
	LoadConfig()
	InitDB()
	initRedis()
	initStorage()
}

func InitDatabaseConfig() {
	LoadConfig()
	InitDB()
}

func LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.AddConfigPath("./config")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}
	AppConfig = &Config{}
	if err := viper.Unmarshal(AppConfig); err != nil {
		log.Fatalf("Unable to decode into struct: %v", err)
	}
	AppConfig.RecommendationPresence = recommendationSettingPresence(viper.GetViper())
	applySensitiveEnvironmentOverrides(AppConfig)
}

func applySensitiveEnvironmentOverrides(cfg *Config) {
	if brokers := parseCSVEnvironment("KAFKA_BROKERS"); len(brokers) > 0 {
		cfg.Kafka.Brokers = brokers
	}
	if value := strings.TrimSpace(os.Getenv("DATABASE_DSN")); value != "" {
		cfg.Database.Dsn = value
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_ENABLED")); value != "" {
		cfg.Embedding.Enabled = strings.EqualFold(value, "true") || value == "1"
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL")); value != "" {
		cfg.Embedding.BaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_API_KEY")); value != "" {
		cfg.Embedding.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL")); value != "" {
		cfg.Embedding.Model = value
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_VERSION")); value != "" {
		cfg.Embedding.Version = value
	}
	if value := strings.TrimSpace(os.Getenv("EMBEDDING_TIMEOUT_SECONDS")); value != "" {
		if parsed := parsePositiveInt(value); parsed > 0 {
			cfg.Embedding.TimeoutSeconds = parsed
		}
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")); value != "" {
		cfg.Storage.Endpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY")); value != "" {
		cfg.Storage.AccessKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY")); value != "" {
		cfg.Storage.SecretKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_BUCKET")); value != "" {
		cfg.Storage.Bucket = value
	}
}

func parseCSVEnvironment(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parsePositiveInt(raw string) int {
	value := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func InitDB() {
	initDB()
}
