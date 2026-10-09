package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"

	"github.com/spf13/viper"
)

func TestConfigLoadingDoesNotInitializeExternalResources(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte("app:\n  port: ':3000'\ndatabase:\n  maxopenconns: 4\n  maxidleconns: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousConfig := AppConfig
	previousAPIDB, previousWorkerDB, previousMaintenanceDB := global.APIDb, global.WorkerDb, global.MaintenanceDb
	previousRedis, previousStorage := global.RedisDB, global.MinioClient
	global.APIDb, global.WorkerDb, global.MaintenanceDb = nil, nil, nil
	global.RedisDB, global.MinioClient = nil, nil
	t.Cleanup(func() {
		AppConfig = previousConfig
		global.APIDb, global.WorkerDb, global.MaintenanceDb = previousAPIDB, previousWorkerDB, previousMaintenanceDB
		global.RedisDB, global.MinioClient = previousRedis, previousStorage
	})

	cfg, err := loadConfigFrom(configPath)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if cfg.Database.MaxOpenConns != 4 || AppConfig != cfg {
		t.Fatalf("loaded config was not returned and published: cfg=%+v AppConfig=%p", cfg.Database, AppConfig)
	}
	if global.APIDb != nil || global.WorkerDb != nil || global.MaintenanceDb != nil || global.RedisDB != nil || global.MinioClient != nil {
		t.Fatal("loading config initialized an external resource")
	}
}

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
	t.Setenv("EMBEDDING_BUILD_VERSION", " post_embedding_build_test ")
	t.Setenv("EMBEDDING_TIMEOUT_SECONDS", "15")
	t.Setenv("MINIO_ACCESS_KEY", "runtime-access")
	t.Setenv("MINIO_SECRET_KEY", "runtime-secret")
	t.Setenv("KAFKA_BROKERS", " kafka-1:9092, ,kafka-2:9092 ")

	cfg := &Config{}
	applySensitiveEnvironmentOverrides(cfg)
	if cfg.Database.Dsn != "postgres://runtime" {
		t.Fatalf("database dsn=%q", cfg.Database.Dsn)
	}
	if !cfg.Embedding.Enabled || cfg.Embedding.BaseURL != "https://embedding.example" || cfg.Embedding.APIKey != "runtime-embedding-key" || cfg.Embedding.Model != "text-embedding-3-small" || cfg.Embedding.BuildVersion != "post_embedding_build_test" || cfg.Embedding.TimeoutSeconds != 15 {
		t.Fatalf("embedding config=%+v", cfg.Embedding)
	}
	if cfg.Storage.AccessKey != "runtime-access" || cfg.Storage.SecretKey != "runtime-secret" {
		t.Fatalf("storage credentials were not overridden")
	}
	if got, want := cfg.Kafka.Brokers, []string{"kafka-1:9092", "kafka-2:9092"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Kafka brokers=%v want=%v", got, want)
	}
}

func TestTranslationConfigDefaultsAndEnvironmentOverrides(t *testing.T) {
	defaults := (TranslationConfig{}).Normalized()
	if defaults.BaseURL != "" ||
		defaults.Model != DefaultTranslationModel ||
		defaults.PromptVersion != DefaultTranslationPromptVersion ||
		defaults.TimeoutSeconds != DefaultTranslationTimeoutSeconds ||
		defaults.CacheTTLHours != DefaultTranslationCacheTTLHours ||
		defaults.CacheJitterHours != DefaultTranslationCacheJitterHours ||
		defaults.MaxSourceRunes != DefaultTranslationMaxSourceRunes ||
		defaults.MaxCompletionTokens != DefaultTranslationMaxCompletionTokens {
		t.Fatalf("translation defaults = %+v", defaults)
	}

	t.Setenv("TRANSLATION_ENABLED", "true")
	t.Setenv("TRANSLATION_BASE_URL", "https://api.cloudflare.com/client/v4/accounts/test-account/ai/v1")
	t.Setenv("TRANSLATION_API_KEY", " runtime-key ")
	t.Setenv("TRANSLATION_MODEL", "model-b")
	t.Setenv("TRANSLATION_PROMPT_VERSION", "social_test")
	t.Setenv("TRANSLATION_TIMEOUT_SECONDS", "12")
	t.Setenv("TRANSLATION_CACHE_TTL_HOURS", "72")
	t.Setenv("TRANSLATION_CACHE_JITTER_HOURS", "6")
	t.Setenv("TRANSLATION_MAX_SOURCE_RUNES", "1500")
	t.Setenv("TRANSLATION_MAX_COMPLETION_TOKENS", "512")

	cfg := &Config{}
	applySensitiveEnvironmentOverrides(cfg)
	got := cfg.Translation.Normalized()
	if !got.Enabled || got.BaseURL != "https://api.cloudflare.com/client/v4/accounts/test-account/ai/v1" || got.APIKey != "runtime-key" ||
		got.Model != "model-b" || got.PromptVersion != "social_test" || got.TimeoutSeconds != 12 ||
		got.CacheTTLHours != 72 || got.CacheJitterHours != 6 || got.MaxSourceRunes != 1500 ||
		got.MaxCompletionTokens != 512 {
		t.Fatalf("translation overrides = %+v", got)
	}
}

func TestValidateEventingConfigByRuntime(t *testing.T) {
	t.Setenv("LIKE_STATE_EXPIRY_ENABLED", "false")
	cfg := &Config{Kafka: KafkaConfig{
		ActivityEventsTopic: "activity",
		ConsumerDLQTopic:    "consumer-dlq",
		NotificationGroupID: "notifications",
	}}
	if err := ValidateAPIEventingConfig(cfg); err != nil {
		t.Fatalf("valid API config error=%v", err)
	}
	if err := ValidateWorkerEventingConfig(cfg); err != nil {
		t.Fatalf("valid Worker config error=%v", err)
	}

	cfg.Kafka.ActivityEventsTopic = ""
	if err := ValidateAPIEventingConfig(cfg); err == nil {
		t.Fatal("API without activity topic must fail")
	}
	cfg.Kafka.ActivityEventsTopic = "activity"
	cfg.Kafka.ConsumerDLQTopic = ""
	if err := ValidateAPIEventingConfig(cfg); err != nil {
		t.Fatalf("API should not require consumer DLQ topic: %v", err)
	}
	if err := ValidateWorkerEventingConfig(cfg); err == nil {
		t.Fatal("Worker without consumer DLQ topic must fail")
	}
	cfg.Kafka.ConsumerDLQTopic = "consumer-dlq"
	cfg.Kafka.NotificationGroupID = ""
	if err := ValidateWorkerEventingConfig(cfg); err == nil {
		t.Fatal("worker without notification group must fail")
	}
}

func TestValidateEventingRejectsLikeStateExpiryUntilSPEC02(t *testing.T) {
	t.Setenv("LIKE_STATE_EXPIRY_ENABLED", "true")
	cfg := &Config{Kafka: KafkaConfig{ActivityEventsTopic: "activity", ConsumerDLQTopic: "consumer-dlq", NotificationGroupID: "notifications"}}
	if err := ValidateAPIEventingConfig(cfg); err == nil || !strings.Contains(err.Error(), "SPEC-02") {
		t.Fatalf("API expiry validation error=%v want SPEC-02 rejection", err)
	}
	if err := ValidateWorkerEventingConfig(cfg); err == nil || !strings.Contains(err.Error(), "SPEC-02") {
		t.Fatalf("worker expiry validation error=%v want SPEC-02 rejection", err)
	}
}

func TestBuildEmbeddingVersionUsesStaticConfiguration(t *testing.T) {
	original := AppConfig
	t.Cleanup(func() { AppConfig = original })

	AppConfig = nil
	if got := BuildEmbeddingVersion(); got != DefaultBuildEmbeddingVersion {
		t.Fatalf("default build version=%q want=%q", got, DefaultBuildEmbeddingVersion)
	}
	AppConfig = &Config{Embedding: EmbeddingConfig{BuildVersion: " post_embedding_v2 "}}
	if got := BuildEmbeddingVersion(); got != "post_embedding_v2" {
		t.Fatalf("configured build version=%q", got)
	}
	AppConfig = &Config{Embedding: EmbeddingConfig{}}
	if got := BuildEmbeddingVersion(); got != DefaultBuildEmbeddingVersion {
		t.Fatalf("empty build version must use its default, got=%q", got)
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

func TestRecommendationTraceConfigDefaultsAndNormalizesCleanupValues(t *testing.T) {
	defaults := (RecommendationTraceConfig{}).Normalized()
	want := RecommendationTraceConfig{
		ResultRetentionDays:           DefaultRecommendationTraceResultRetentionDays,
		RequestRetentionDays:          DefaultRecommendationTraceRequestRetentionDays,
		CleanupIntervalSeconds:        DefaultRecommendationTraceCleanupIntervalSeconds,
		CleanupCatchupIntervalSeconds: DefaultRecommendationTraceCleanupCatchupIntervalSeconds,
		CleanupResultBatchSize:        DefaultRecommendationTraceCleanupResultBatchSize,
		CleanupRequestBatchSize:       DefaultRecommendationTraceCleanupRequestBatchSize,
		CleanupRunBudgetSeconds:       DefaultRecommendationTraceCleanupRunBudgetSeconds,
		CleanupMaxResultRowsPerRun:    DefaultRecommendationTraceCleanupMaxResultRowsPerRun,
		CleanupMaxRequestRowsPerRun:   DefaultRecommendationTraceCleanupMaxRequestRowsPerRun,
	}
	if defaults != want {
		t.Fatalf("default recommendation trace config=%+v want=%+v", defaults, want)
	}

	partial := (RecommendationTraceConfig{
		ResultRetentionDays:           -1,
		RequestRetentionDays:          30,
		CleanupIntervalSeconds:        120,
		CleanupCatchupIntervalSeconds: 0,
		CleanupResultBatchSize:        -5,
		CleanupRequestBatchSize:       25,
		CleanupRunBudgetSeconds:       -1,
		CleanupMaxResultRowsPerRun:    500,
		CleanupMaxRequestRowsPerRun:   0,
	}).Normalized()
	if partial.ResultRetentionDays != want.ResultRetentionDays || partial.RequestRetentionDays != want.RequestRetentionDays ||
		partial.CleanupIntervalSeconds != 120 || partial.CleanupCatchupIntervalSeconds != want.CleanupCatchupIntervalSeconds ||
		partial.CleanupResultBatchSize != want.CleanupResultBatchSize || partial.CleanupRequestBatchSize != 25 ||
		partial.CleanupRunBudgetSeconds != want.CleanupRunBudgetSeconds || partial.CleanupMaxResultRowsPerRun != 500 ||
		partial.CleanupMaxRequestRowsPerRun != want.CleanupMaxRequestRowsPerRun {
		t.Fatalf("partially normalized recommendation trace config=%+v", partial)
	}

	custom := (RecommendationTraceConfig{
		ResultRetentionDays: 60, RequestRetentionDays: 90,
		CleanupIntervalSeconds: 300, CleanupCatchupIntervalSeconds: 30,
		CleanupResultBatchSize: 100, CleanupRequestBatchSize: 20,
		CleanupRunBudgetSeconds: 15, CleanupMaxResultRowsPerRun: 1000,
		CleanupMaxRequestRowsPerRun: 100,
	}).Normalized()
	if custom.ResultRetentionDays != 60 || custom.RequestRetentionDays != 90 || custom.CleanupIntervalSeconds != 300 ||
		custom.CleanupCatchupIntervalSeconds != 30 || custom.CleanupResultBatchSize != 100 ||
		custom.CleanupRequestBatchSize != 20 || custom.CleanupRunBudgetSeconds != 15 ||
		custom.CleanupMaxResultRowsPerRun != 1000 || custom.CleanupMaxRequestRowsPerRun != 100 {
		t.Fatalf("custom recommendation trace config changed: %+v", custom)
	}
}

func TestLikeStateEnvironmentDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{
		"LIKE_STATE_EXPIRY_ENABLED",
		"LIKE_STATE_IDLE_BEFORE_EXPIRY",
		"LIKE_STATE_TTL",
		"LIKE_STATE_READ_RENEWAL_THRESHOLD",
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
	if got := LikeStateReadRenewalThreshold(); got != 12*time.Hour {
		t.Fatalf("read renewal threshold=%s want 12h", got)
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
	t.Setenv("LIKE_STATE_READ_RENEWAL_THRESHOLD", "")
	if got := LikeStateReadRenewalThreshold(); got != 45*time.Minute {
		t.Fatalf("custom TTL renewal default=%s want 45m", got)
	}
	t.Setenv("LIKE_STATE_READ_RENEWAL_THRESHOLD", "30m")
	t.Setenv("LIKE_STATE_MAINTENANCE_INTERVAL", "15s")
	t.Setenv("LIKE_STATE_MAINTENANCE_BATCH_SIZE", "25")
	if !LikeStateExpiryEnabled() || LikeStateIdleBeforeExpiry() != 2*time.Hour || LikeStateTTL() != 90*time.Minute || LikeStateReadRenewalThreshold() != 30*time.Minute || LikeStateMaintenanceInterval() != 15*time.Second || LikeStateMaintenanceBatchSize() != 25 {
		t.Fatalf("unexpected like state config enabled=%t idle=%s ttl=%s renewal=%s interval=%s batch=%d", LikeStateExpiryEnabled(), LikeStateIdleBeforeExpiry(), LikeStateTTL(), LikeStateReadRenewalThreshold(), LikeStateMaintenanceInterval(), LikeStateMaintenanceBatchSize())
	}

	for _, invalid := range []string{"90m", "0s", "not-a-duration"} {
		t.Setenv("LIKE_STATE_READ_RENEWAL_THRESHOLD", invalid)
		if got := LikeStateReadRenewalThreshold(); got != 45*time.Minute {
			t.Fatalf("invalid renewal threshold %q=%s want TTL/2", invalid, got)
		}
	}

	t.Setenv("LIKE_STATE_MAINTENANCE_BATCH_SIZE", "0")
	if got := LikeStateMaintenanceBatchSize(); got != 1 {
		t.Fatalf("non-positive batch=%d want=1", got)
	}
}
