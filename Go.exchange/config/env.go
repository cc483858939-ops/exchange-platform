package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
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

// ValidateRuntimeEventingConfig fails before a process can accept authoritative
// mutations while its required durable activity path is unavailable.
func ValidateRuntimeEventingConfig(role string) error {
	if AppConfig == nil {
		return errors.New("application configuration is not initialized")
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = RuntimeRoleAll
	}
	if role != RuntimeRoleAPI && role != RuntimeRoleWorker && role != RuntimeRoleAll {
		role = RuntimeRoleAll
	}
	if strings.TrimSpace(AppConfig.Kafka.ActivityEventsTopic) == "" {
		return errors.New("Kafka activity events topic is not configured")
	}
	if role == RuntimeRoleWorker || role == RuntimeRoleAll {
		if strings.TrimSpace(AppConfig.Kafka.NotificationGroupID) == "" {
			return errors.New("Kafka notification consumer group is not configured")
		}
		if strings.TrimSpace(AppConfig.Kafka.NotificationDLQTopic) == "" {
			return errors.New("Kafka notification DLQ topic is not configured")
		}
	}
	return nil
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

// TrustedProxyCIDRs returns the explicitly configured network origins that may
// supply an alternative client IP through the supported forwarding headers.
// An empty configuration deliberately means that no proxy is trusted.
func TrustedProxyCIDRs() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if raw == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}

		canonical, err := canonicalTrustedProxyCIDR(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
	}

	if len(seen) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalTrustedProxyCIDR(value string) (string, error) {
	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return "", fmt.Errorf("invalid TRUSTED_PROXY_CIDRS entry")
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return "", fmt.Errorf("TRUSTED_PROXY_CIDRS must not contain a trust-all network")
		}
		return network.String(), nil
	}

	ip := net.ParseIP(value)
	if ip == nil {
		return "", fmt.Errorf("invalid TRUSTED_PROXY_CIDRS entry")
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.String() + "/32", nil
	}
	return ip.String() + "/128", nil
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

func LikeStateExpiryEnabled() bool {
	return envBool("LIKE_STATE_EXPIRY_ENABLED", false)
}

func LikeStateIdleBeforeExpiry() time.Duration {
	return envDuration("LIKE_STATE_IDLE_BEFORE_EXPIRY", time.Hour)
}

func LikeStateTTL() time.Duration {
	return envDuration("LIKE_STATE_TTL", 24*time.Hour)
}

func LikeStateMaintenanceInterval() time.Duration {
	return envDuration("LIKE_STATE_MAINTENANCE_INTERVAL", time.Minute)
}

func LikeStateMaintenanceBatchSize() int {
	batch := envInt("LIKE_STATE_MAINTENANCE_BATCH_SIZE", 100)
	if batch < 1 {
		return 1
	}
	return batch
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

func PostViewEventsPerMinute() int {
	limit := envInt("POST_VIEW_EVENTS_PER_MINUTE", 300)
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
