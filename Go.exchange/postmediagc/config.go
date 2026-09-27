package postmediagc

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBatchSize     = 100
	defaultMaxRows       = 1000
	defaultClaimTimeout  = 15 * time.Minute
	defaultRetryBase     = 5 * time.Minute
	defaultRetryMax      = 6 * time.Hour
	defaultObjectTimeout = 30 * time.Second
	defaultRunTimeout    = 8 * time.Minute
	maxRowsPerRun        = 10000
)

type Config struct {
	BatchSize     int
	MaxRowsPerRun int
	ClaimTimeout  time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
	ObjectTimeout time.Duration
	RunTimeout    time.Duration
}

func DefaultConfig() Config {
	return Config{
		BatchSize:     defaultBatchSize,
		MaxRowsPerRun: defaultMaxRows,
		ClaimTimeout:  defaultClaimTimeout,
		RetryBase:     defaultRetryBase,
		RetryMax:      defaultRetryMax,
		ObjectTimeout: defaultObjectTimeout,
		RunTimeout:    defaultRunTimeout,
	}
}

// LoadConfigFromEnv falls back to safe defaults for malformed or out-of-range
// values so a typo cannot turn a scheduled cleanup into an unbounded drain.
func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.BatchSize = boundedIntEnv("POST_MEDIA_GC_BATCH_SIZE", cfg.BatchSize, 1, 1000)
	cfg.MaxRowsPerRun = boundedIntEnv("POST_MEDIA_GC_MAX_ROWS_PER_RUN", cfg.MaxRowsPerRun, 1, maxRowsPerRun)
	cfg.ClaimTimeout = boundedDurationEnv("POST_MEDIA_GC_CLAIM_TIMEOUT", cfg.ClaimTimeout, time.Second, 24*time.Hour)
	cfg.RetryBase = boundedDurationEnv("POST_MEDIA_GC_RETRY_BASE", cfg.RetryBase, time.Second, 6*time.Hour)
	cfg.RetryMax = boundedDurationEnv("POST_MEDIA_GC_RETRY_MAX", cfg.RetryMax, time.Second, 24*time.Hour)
	cfg.ObjectTimeout = boundedDurationEnv("POST_MEDIA_GC_OBJECT_TIMEOUT", cfg.ObjectTimeout, time.Second, 5*time.Minute)
	cfg.RunTimeout = boundedDurationEnv("POST_MEDIA_GC_RUN_TIMEOUT", cfg.RunTimeout, time.Second, 9*time.Minute)
	if cfg.RetryMax < cfg.RetryBase {
		cfg.RetryBase = defaultRetryBase
		cfg.RetryMax = defaultRetryMax
	}
	if cfg.ClaimTimeout <= cfg.RunTimeout {
		cfg.ClaimTimeout = defaultClaimTimeout
		cfg.RunTimeout = defaultRunTimeout
	}
	return cfg
}

func (cfg Config) Validate() error {
	if cfg.BatchSize < 1 || cfg.BatchSize > 1000 {
		return configError("batch size must be between 1 and 1000")
	}
	if cfg.MaxRowsPerRun < 1 || cfg.MaxRowsPerRun > maxRowsPerRun {
		return configError("maximum rows per run must be between 1 and 10000")
	}
	if cfg.ClaimTimeout < time.Second || cfg.ClaimTimeout > 24*time.Hour {
		return configError("claim timeout must be between 1s and 24h")
	}
	if cfg.ClaimTimeout <= cfg.RunTimeout {
		return configError("claim timeout must exceed the maximum run timeout")
	}
	if cfg.RetryBase < time.Second || cfg.RetryBase > 6*time.Hour || cfg.RetryMax < cfg.RetryBase || cfg.RetryMax > 24*time.Hour {
		return configError("retry delays must satisfy 1s <= base <= 6h and base <= max <= 24h")
	}
	if cfg.ObjectTimeout < time.Second || cfg.ObjectTimeout > 5*time.Minute {
		return configError("object timeout must be between 1s and 5m")
	}
	if cfg.RunTimeout < time.Second || cfg.RunTimeout > 9*time.Minute {
		return configError("run timeout must be between 1s and 9m")
	}
	return nil
}

type configError string

func (e configError) Error() string { return string(e) }

func boundedIntEnv(name string, fallback, minimum, maximum int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func boundedDurationEnv(name string, fallback, minimum, maximum time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func RetryDelay(attempt int64, base, maximum time.Duration) time.Duration {
	if attempt <= 0 || base <= 0 || maximum <= 0 {
		return 0
	}
	delay := base
	for step := int64(1); step < attempt; step++ {
		if delay >= maximum || delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
