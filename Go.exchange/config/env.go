package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RuntimeRoleAll    = "all"
	RuntimeRoleAPI    = "api"
	RuntimeRoleWorker = "worker"
)

func RuntimeRole() string {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("APP_RUNTIME_ROLE")))
	switch role {
	case "", RuntimeRoleAll:
		return RuntimeRoleAll
	case RuntimeRoleAPI:
		return RuntimeRoleAPI
	case RuntimeRoleWorker:
		return RuntimeRoleWorker
	default:
		return RuntimeRoleAll
	}
}

func AppPort() string {
	port := strings.TrimSpace(os.Getenv("APP_PORT"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("PORT"))
	}
	if port == "" && AppConfig != nil {
		port = strings.TrimSpace(AppConfig.App.Port)
	}
	if port == "" {
		port = "3000"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
	return port
}

func WorkerHealthAddr() string {
	port := strings.TrimSpace(os.Getenv("WORKER_HEALTH_PORT"))
	if port == "" {
		port = "8081"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
	return port
}
func DatabaseDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN")); dsn != "" {
		return dsn
	}
	if AppConfig != nil {
		return AppConfig.Database.Dsn
	}
	return ""
}

func RedisAddr() string {
	if addr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); addr != "" {
		return addr
	}
	return "redis:6379"
}

func RedisPassword() string {
	return strings.TrimSpace(os.Getenv("REDIS_PASSWORD"))
}

func RedisDB() int {
	return envInt("REDIS_DB", 0)
}

func RedisPoolSize() int {
	return envInt("REDIS_POOL_SIZE", 1000)
}

func RedisMinIdleConns() int {
	return envInt("REDIS_MIN_IDLE_CONNS", 50)
}

func LikeSnapshotPollInterval() time.Duration {
	return envDuration("LIKE_SNAPSHOT_POLL_INTERVAL", time.Second)
}

func LikeSnapshotBatchSize() int    { return envInt("LIKE_SNAPSHOT_BATCH_SIZE", 100) }
func LikeClaimLease() time.Duration { return envDuration("LIKE_CLAIM_LEASE", 30*time.Second) }

func LikeBehaviorBatchSize() int {
	batch := envInt("LIKE_BEHAVIOR_BATCH_SIZE", 500)
	if batch < 1 {
		return 1
	}
	return batch
}

func LikeBehaviorClaimLease() time.Duration {
	return envDuration("LIKE_BEHAVIOR_CLAIM_LEASE", 30*time.Second)
}

func LikeBehaviorFlushInterval() time.Duration {
	return envDuration("LIKE_BEHAVIOR_FLUSH_INTERVAL", time.Second)
}

func LikeBehaviorProjectionConsumers() int {
	consumers := envInt("LIKE_BEHAVIOR_PROJECTION_CONSUMERS", 6)
	if consumers < 1 {
		return 1
	}
	return consumers
}

func NotificationProjectionConsumers() int {
	consumers := envInt("NOTIFICATION_PROJECTION_CONSUMERS", 1)
	if consumers < 1 {
		return 1
	}
	return consumers
}

func RecommendationTelemetryEnabled() bool {
	return envBool("RECOMMENDATION_TELEMETRY_ENABLED", false)
}

func RecommendationTelemetryRolloutPercent() int {
	percent := envInt("RECOMMENDATION_TELEMETRY_ROLLOUT_PERCENT", 0)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func RecommendationTelemetrySigningKey() string {
	return strings.TrimSpace(os.Getenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY"))
}

func RecommendationTelemetryTokenTTL() time.Duration {
	return envDuration("RECOMMENDATION_TELEMETRY_TOKEN_TTL", 24*time.Hour)
}

func RecommendationTelemetryMaxClockSkew() time.Duration {
	return envDuration("RECOMMENDATION_TELEMETRY_MAX_CLOCK_SKEW", 5*time.Minute)
}

func RecommendationTelemetryEventsPerMinute() int {
	limit := envInt("RECOMMENDATION_TELEMETRY_EVENTS_PER_MINUTE", 1000)
	if limit < 1 {
		return 1
	}
	return limit
}

func ArticleViewEventsPerMinute() int {
	limit := envInt("ARTICLE_VIEW_EVENTS_PER_MINUTE", 300)
	if limit < 1 {
		return 1
	}
	return limit
}

func StorageEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	if AppConfig != nil && strings.TrimSpace(AppConfig.Storage.Endpoint) != "" {
		return strings.TrimSpace(AppConfig.Storage.Endpoint)
	}
	return "minio:9000"
}

func StorageAccessKey() string {
	if accessKey := strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY")); accessKey != "" {
		return accessKey
	}
	if AppConfig != nil && strings.TrimSpace(AppConfig.Storage.AccessKey) != "" {
		return strings.TrimSpace(AppConfig.Storage.AccessKey)
	}
	return "minioadmin"
}

func StorageSecretKey() string {
	if secretKey := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY")); secretKey != "" {
		return secretKey
	}
	if AppConfig != nil && strings.TrimSpace(AppConfig.Storage.SecretKey) != "" {
		return strings.TrimSpace(AppConfig.Storage.SecretKey)
	}
	return "minioadmin"
}

func StorageBucket() string {
	if bucket := strings.TrimSpace(os.Getenv("MINIO_BUCKET")); bucket != "" {
		return bucket
	}
	if AppConfig != nil && strings.TrimSpace(AppConfig.Storage.Bucket) != "" {
		return strings.TrimSpace(AppConfig.Storage.Bucket)
	}
	return "go-exchange"
}

func StorageUseSSL() bool {
	if raw := strings.TrimSpace(os.Getenv("MINIO_USE_SSL")); raw != "" {
		return envBool("MINIO_USE_SSL", false)
	}
	if AppConfig != nil {
		return AppConfig.Storage.UseSSL
	}
	return false
}

// ExchangeRateEndpoint is configured by environment variable so deployments
// can switch providers without putting credentials in config.yml.
func ExchangeRateEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv("EXCHANGE_RATE_API_URL")); endpoint != "" {
		return endpoint
	}
	return "https://api.frankfurter.dev/v2/rates"
}

func ExchangeRateProvider() string {
	if provider := strings.TrimSpace(os.Getenv("EXCHANGE_RATE_PROVIDER")); provider != "" {
		return strings.ToUpper(provider)
	}
	return "ECB"
}

func ExchangeRateBaseCurrency() string {
	if currency := strings.TrimSpace(os.Getenv("EXCHANGE_RATE_BASE")); currency != "" {
		return strings.ToUpper(currency)
	}
	return "EUR"
}

func ExchangeRateRefreshInterval() time.Duration {
	return envDuration("EXCHANGE_RATE_REFRESH_INTERVAL", 15*time.Minute)
}

func ExchangeRateFreshFor() time.Duration {
	return envDuration("EXCHANGE_RATE_FRESH_FOR", 30*time.Minute)
}

func ExchangeRateMaxStale() time.Duration {
	return envDuration("EXCHANGE_RATE_MAX_STALE", 24*time.Hour)
}

func ExchangeRateRequestTimeout() time.Duration {
	return envDuration("EXCHANGE_RATE_REQUEST_TIMEOUT", 8*time.Second)
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}
