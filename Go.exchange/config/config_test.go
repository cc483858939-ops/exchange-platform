package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestRecommendationSettingPresenceDetectsExplicitZeroAndFalse(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(`
recommendation:
  following_bonus: 0
  out_of_network_min_ratio: 0
  diversity:
    enabled: false
    semantic_duplicate_penalty: 0
`)); err != nil {
		t.Fatal(err)
	}

	presence := recommendationSettingPresence(v)
	for _, key := range []string{
		"following_bonus",
		"out_of_network_min_ratio",
		"diversity.enabled",
		"diversity.semantic_duplicate_penalty",
	} {
		if !presence[key] {
			t.Fatalf("presence[%q]=false, want true", key)
		}
	}
	if presence["semantic_weight"] {
		t.Fatal("semantic_weight must be absent")
	}
}

func TestRecommendationSettingPresenceIgnoresViperDefaults(t *testing.T) {
	v := viper.New()
	v.SetDefault("recommendation.following_bonus", 0.5)

	presence := recommendationSettingPresence(v)
	if presence["following_bonus"] {
		t.Fatal("Viper default must not count as config-file presence")
	}
}

func TestHasRecommendationSetting(t *testing.T) {
	var nilConfig *Config
	if nilConfig.HasRecommendationSetting("following_bonus") {
		t.Fatal("nil Config must report false")
	}
	if (&Config{}).HasRecommendationSetting("following_bonus") {
		t.Fatal("nil presence map must report false")
	}
	cfg := &Config{RecommendationPresence: map[string]bool{"following_bonus": true}}
	if !cfg.HasRecommendationSetting("  FOLLOWING_BONUS ") {
		t.Fatal("known key should be case and whitespace normalized")
	}
	if cfg.HasRecommendationSetting("unknown") || cfg.HasRecommendationSetting(" ") {
		t.Fatal("unknown and empty keys must report false")
	}
}

func TestApplySensitiveEnvironmentOverrides(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://runtime")
	t.Setenv("EMBEDDING_ENABLED", "true")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example")
	t.Setenv("EMBEDDING_API_KEY", "runtime-embedding-key")
	t.Setenv("EMBEDDING_MODEL", "text-embedding-3-small")
	t.Setenv("EMBEDDING_VERSION", "post_embedding_test")
	t.Setenv("EMBEDDING_TIMEOUT_SECONDS", "15")
	t.Setenv("MINIO_ACCESS_KEY", "runtime-access")
	t.Setenv("MINIO_SECRET_KEY", "runtime-secret")
	t.Setenv("KAFKA_BROKERS", " kafka-1:9092, ,kafka-2:9092 ")

	cfg := &Config{}
	applySensitiveEnvironmentOverrides(cfg)
	if cfg.Database.Dsn != "postgres://runtime" {
		t.Fatalf("database dsn=%q", cfg.Database.Dsn)
	}
	if !cfg.Embedding.Enabled || cfg.Embedding.BaseURL != "https://embedding.example" || cfg.Embedding.APIKey != "runtime-embedding-key" || cfg.Embedding.Model != "text-embedding-3-small" || cfg.Embedding.Version != "post_embedding_test" || cfg.Embedding.TimeoutSeconds != 15 {
		t.Fatalf("embedding config=%+v", cfg.Embedding)
	}
	if cfg.Storage.AccessKey != "runtime-access" || cfg.Storage.SecretKey != "runtime-secret" {
		t.Fatalf("storage credentials were not overridden")
	}
	if got, want := cfg.Kafka.Brokers, []string{"kafka-1:9092", "kafka-2:9092"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Kafka brokers=%v want=%v", got, want)
	}
}

func TestValidateRuntimeEventingConfigByRole(t *testing.T) {
	original := AppConfig
	t.Cleanup(func() { AppConfig = original })

	AppConfig = &Config{Kafka: KafkaConfig{
		ActivityEventsTopic:  "activity",
		NotificationGroupID:  "notifications",
		NotificationDLQTopic: "notification-dlq",
	}}
	for _, role := range []string{RuntimeRoleAPI, RuntimeRoleWorker, RuntimeRoleAll} {
		if err := ValidateRuntimeEventingConfig(role); err != nil {
			t.Fatalf("role=%s error=%v", role, err)
		}
	}

	AppConfig.Kafka.ActivityEventsTopic = ""
	if err := ValidateRuntimeEventingConfig(RuntimeRoleAPI); err == nil {
		t.Fatal("API without activity topic must fail")
	}
	AppConfig.Kafka.ActivityEventsTopic = "activity"
	AppConfig.Kafka.NotificationGroupID = ""
	if err := ValidateRuntimeEventingConfig(RuntimeRoleWorker); err == nil {
		t.Fatal("worker without notification group must fail")
	}
	AppConfig.Kafka.NotificationGroupID = "notifications"
	AppConfig.Kafka.NotificationDLQTopic = ""
	if err := ValidateRuntimeEventingConfig(RuntimeRoleAll); err == nil {
		t.Fatal("all role without notification DLQ must fail")
	}
}

func TestActiveEmbeddingVersionUsesConfiguredValueAndDefault(t *testing.T) {
	original := AppConfig
	t.Cleanup(func() { AppConfig = original })

	AppConfig = nil
	if got := ActiveEmbeddingVersion(); got != DefaultEmbeddingVersion {
		t.Fatalf("default version=%q want=%q", got, DefaultEmbeddingVersion)
	}
	AppConfig = &Config{Embedding: EmbeddingConfig{Version: "  post_embedding_v2  "}}
	if got := ActiveEmbeddingVersion(); got != "post_embedding_v2" {
		t.Fatalf("configured version=%q", got)
	}
	AppConfig = &Config{}
	if got := ActiveEmbeddingVersion(); got != DefaultEmbeddingVersion {
		t.Fatalf("empty version=%q want=%q", got, DefaultEmbeddingVersion)
	}
}

func TestRecommendationProfileMaterializationDefaultsAllNonPositiveValues(t *testing.T) {
	got := (RecommendationProfileMaterializationConfig{}).Normalized()
	want := RecommendationProfileMaterializationConfig{
		DebounceSeconds:          DefaultRecommendationProfileDebounceSeconds,
		PollIntervalSeconds:      DefaultRecommendationProfilePollIntervalSeconds,
		BatchSize:                DefaultRecommendationProfileBatchSize,
		RebuildIntervalHours:     DefaultRecommendationProfileRebuildIntervalHours,
		StaleScanIntervalSeconds: DefaultRecommendationProfileStaleScanIntervalSeconds,
		StaleEnqueueBatchSize:    DefaultRecommendationProfileStaleEnqueueBatchSize,
	}
	if got != want {
		t.Fatalf("normalized profile materialization=%+v want=%+v", got, want)
	}
	configured := (RecommendationProfileMaterializationConfig{
		DebounceSeconds: -1, PollIntervalSeconds: 0, BatchSize: 12, RebuildIntervalHours: 3,
		StaleScanIntervalSeconds: -5, StaleEnqueueBatchSize: 7,
	}).Normalized()
	if configured.DebounceSeconds != want.DebounceSeconds || configured.PollIntervalSeconds != want.PollIntervalSeconds || configured.BatchSize != 12 || configured.RebuildIntervalHours != 3 || configured.StaleScanIntervalSeconds != want.StaleScanIntervalSeconds || configured.StaleEnqueueBatchSize != 7 {
		t.Fatalf("partial defaults=%+v", configured)
	}
}

func TestLikeStateEnvironmentDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{
		"LIKE_STATE_EXPIRY_ENABLED",
		"LIKE_STATE_IDLE_BEFORE_EXPIRY",
		"LIKE_STATE_TTL",
		"LIKE_STATE_MAINTENANCE_INTERVAL",
		"LIKE_STATE_MAINTENANCE_BATCH_SIZE",
	} {
		t.Setenv(key, "")
	}
	if LikeStateExpiryEnabled() {
		t.Fatal("expiry must default to disabled")
	}
	if got := LikeStateIdleBeforeExpiry(); got != time.Hour {
		t.Fatalf("idle threshold=%s", got)
	}
	if got := LikeStateTTL(); got != 24*time.Hour {
		t.Fatalf("TTL=%s", got)
	}
	if got := LikeStateMaintenanceInterval(); got != time.Minute {
		t.Fatalf("maintenance interval=%s", got)
	}
	if got := LikeStateMaintenanceBatchSize(); got != 100 {
		t.Fatalf("maintenance batch=%d", got)
	}

	t.Setenv("LIKE_STATE_EXPIRY_ENABLED", "true")
	t.Setenv("LIKE_STATE_IDLE_BEFORE_EXPIRY", "2h")
	t.Setenv("LIKE_STATE_TTL", "90m")
	t.Setenv("LIKE_STATE_MAINTENANCE_INTERVAL", "15s")
	t.Setenv("LIKE_STATE_MAINTENANCE_BATCH_SIZE", "25")
	if !LikeStateExpiryEnabled() || LikeStateIdleBeforeExpiry() != 2*time.Hour || LikeStateTTL() != 90*time.Minute || LikeStateMaintenanceInterval() != 15*time.Second || LikeStateMaintenanceBatchSize() != 25 {
		t.Fatalf("unexpected like state config enabled=%t idle=%s ttl=%s interval=%s batch=%d", LikeStateExpiryEnabled(), LikeStateIdleBeforeExpiry(), LikeStateTTL(), LikeStateMaintenanceInterval(), LikeStateMaintenanceBatchSize())
	}

	t.Setenv("LIKE_STATE_MAINTENANCE_BATCH_SIZE", "0")
	if got := LikeStateMaintenanceBatchSize(); got != 1 {
		t.Fatalf("non-positive batch=%d want=1", got)
	}
}
