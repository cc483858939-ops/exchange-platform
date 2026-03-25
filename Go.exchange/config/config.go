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
	AI AIConfig
}

var AppConfig *Config

func InitConfig() {
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
	initDB()
	initRedis()
}
