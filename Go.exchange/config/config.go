package config

import (
	"log"

	"github.com/spf13/viper"
)

// AIConfig holds the runtime settings used by the async article analysis pipeline.
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

type RecommendationBehaviorWeights struct {
	View float64 `mapstructure:"view"`
	Like float64 `mapstructure:"like"`
}

type RecommendationConfig struct {
	BehaviorWeights  RecommendationBehaviorWeights `mapstructure:"behavior_weights"`
	CategoryWeight   float64                       `mapstructure:"category_weight"`
	TagWeight        float64                       `mapstructure:"tag_weight"`
	PopularityWeight float64                       `mapstructure:"popularity_weight"`
	FreshnessWeight  float64                       `mapstructure:"freshness_weight"`
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
}

func InitDB() {
	initDB()
}
