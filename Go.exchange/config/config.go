package config

import (
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type AIConfig struct {
	BaseURL             string `mapstructure:"base_url"`
	APIKey              string `mapstructure:"api_key"`
	Model               string `mapstructure:"model"`
	ChunkModel          string `mapstructure:"chunk_model"`
	MainModel           string `mapstructure:"main_model"`
	TimeoutSeconds      int    `mapstructure:"timeout_seconds"`
	ChunkSize           int    `mapstructure:"chunk_size"`
	ChunkOverlap        int    `mapstructure:"chunk_overlap"`
	MaxChunkParallelism int    `mapstructure:"max_chunk_parallelism"`
	TopNTags            int    `mapstructure:"top_n_tags"`
}

type KafkaConfig struct {
	Brokers                      []string `mapstructure:"brokers"`
	ArticleAnalysisTopic         string   `mapstructure:"article_analysis_topic"`
	ArticleAnalysisDLQTopic      string   `mapstructure:"article_analysis_dlq_topic"`
	UserBehaviorTopic            string   `mapstructure:"user_behavior_topic"`
	LikeSnapshotTopic            string   `mapstructure:"like_snapshot_topic"`
	RecommendationEventsTopic    string   `mapstructure:"recommendation_events_topic"`
	ArticleAnalysisGroupID       string   `mapstructure:"article_analysis_group_id"`
	UserBehaviorGroupID          string   `mapstructure:"user_behavior_group_id"`
	LikeSnapshotGroupID          string   `mapstructure:"like_snapshot_group_id"`
	RecommendationMetricsGroupID string   `mapstructure:"recommendation_metrics_group_id"`
	OutboxPollIntervalSecond     int      `mapstructure:"outbox_poll_interval_seconds"`
	JobLeaseSeconds              int      `mapstructure:"job_lease_seconds"`
	JobMaxAttempts               int      `mapstructure:"job_max_attempts"`
}

type RecommendationBehaviorWeights struct {
	View          float64 `mapstructure:"view"`
	Like          float64 `mapstructure:"like"`
	Click         float64 `mapstructure:"click"`
	QualifiedRead float64 `mapstructure:"qualified_read"`
	QuickBounce   float64 `mapstructure:"quick_bounce"`
	NotInterested float64 `mapstructure:"not_interested"`
}

type RecommendationConfig struct {
	BehaviorWeights         RecommendationBehaviorWeights `mapstructure:"behavior_weights"`
	SignalHalfLifeDays      float64                       `mapstructure:"signal_half_life_days"`
	FeedbackLookbackDays    int                           `mapstructure:"feedback_lookback_days"`
	InterestSaturationScale float64                       `mapstructure:"interest_saturation_scale"`
	CategoryWeight          float64                       `mapstructure:"category_weight"`
	TagWeight               float64                       `mapstructure:"tag_weight"`
	PopularityWeight        float64                       `mapstructure:"popularity_weight"`
	FreshnessWeight         float64                       `mapstructure:"freshness_weight"`
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
	AI             AIConfig
	Kafka          KafkaConfig
	Recommendation RecommendationConfig
	Storage        StorageConfig
}

var AppConfig *Config

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
	applySensitiveEnvironmentOverrides(AppConfig)
}

func applySensitiveEnvironmentOverrides(config *Config) {
	if value := strings.TrimSpace(os.Getenv("DATABASE_DSN")); value != "" {
		config.Database.Dsn = value
	}
	if value := strings.TrimSpace(os.Getenv("AI_BASE_URL")); value != "" {
		config.AI.BaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("AI_API_KEY")); value != "" {
		config.AI.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("AI_MODEL")); value != "" {
		config.AI.Model = value
	}
	if value := strings.TrimSpace(os.Getenv("AI_CHUNK_MODEL")); value != "" {
		config.AI.ChunkModel = value
	}
	if value := strings.TrimSpace(os.Getenv("AI_MAIN_MODEL")); value != "" {
		config.AI.MainModel = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")); value != "" {
		config.Storage.Endpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY")); value != "" {
		config.Storage.AccessKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY")); value != "" {
		config.Storage.SecretKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MINIO_BUCKET")); value != "" {
		config.Storage.Bucket = value
	}
}

func InitDB() {
	initDB()
}
