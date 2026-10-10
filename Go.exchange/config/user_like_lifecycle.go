package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultUserLikeSetTTL                = 72 * time.Hour
	DefaultUserLikeRestoreLockTTL        = 30 * time.Second
	DefaultUserLikeRestoreBatchSize      = 500
	DefaultUserLikeRestoreMaxRelations   = 10000
	DefaultUserLikeRestoreRequestTimeout = 2 * time.Second
	DefaultUserLikeRestoreConcurrency    = 8

	minUserLikeSetTTL       = time.Hour
	maxUserLikeSetTTL       = 365 * 24 * time.Hour
	maxUserLikeRestoreBatch = 5000
	maxUserLikeRestoreRows  = 10000
	maxUserLikeRestoreSlots = 64
)

// UserLikeLifecycleConfig controls only User -> Posts TTL and cold-user
// recovery. It is deliberately independent from the disabled Post-only TTL.
type UserLikeLifecycleConfig struct {
	ArmingEnabled         bool
	RestoreEnabled        bool
	SetTTL                time.Duration
	RestoreLockTTL        time.Duration
	RestoreBatchSize      int
	RestoreMaxRelations   int
	RestoreRequestTimeout time.Duration
	RestoreConcurrency    int
}

// UserLikeLifecycleSettings parses and validates the independent User Like
// lifecycle environment. Invalid values fail closed instead of silently
// selecting a dangerously short TTL or unbounded restore workload.
func UserLikeLifecycleSettings() (UserLikeLifecycleConfig, error) {
	settings := UserLikeLifecycleConfig{
		SetTTL:                DefaultUserLikeSetTTL,
		RestoreLockTTL:        DefaultUserLikeRestoreLockTTL,
		RestoreBatchSize:      DefaultUserLikeRestoreBatchSize,
		RestoreMaxRelations:   DefaultUserLikeRestoreMaxRelations,
		RestoreRequestTimeout: DefaultUserLikeRestoreRequestTimeout,
		RestoreConcurrency:    DefaultUserLikeRestoreConcurrency,
	}
	var err error
	if settings.ArmingEnabled, err = parseBoolSetting("USER_LIKE_TTL_ARMING_ENABLED", true); err != nil {
		return settings, err
	}
	if settings.RestoreEnabled, err = parseBoolSetting("USER_LIKE_TTL_RESTORE_ENABLED", true); err != nil {
		return settings, err
	}
	if settings.SetTTL, err = parseDurationSetting("USER_LIKE_SET_TTL", settings.SetTTL); err != nil {
		return settings, err
	}
	if settings.RestoreLockTTL, err = parseDurationSetting("USER_LIKE_RESTORE_LOCK_TTL", settings.RestoreLockTTL); err != nil {
		return settings, err
	}
	if settings.RestoreRequestTimeout, err = parseDurationSetting("USER_LIKE_RESTORE_REQUEST_TIMEOUT", settings.RestoreRequestTimeout); err != nil {
		return settings, err
	}
	if settings.RestoreBatchSize, err = parseBoundedIntSetting("USER_LIKE_RESTORE_BATCH_SIZE", settings.RestoreBatchSize, 1, maxUserLikeRestoreBatch); err != nil {
		return settings, err
	}
	if settings.RestoreMaxRelations, err = parseBoundedIntSetting("USER_LIKE_RESTORE_MAX_RELATIONS", settings.RestoreMaxRelations, 1, maxUserLikeRestoreRows); err != nil {
		return settings, err
	}
	if settings.RestoreConcurrency, err = parseBoundedIntSetting("USER_LIKE_RESTORE_CONCURRENCY", settings.RestoreConcurrency, 1, maxUserLikeRestoreSlots); err != nil {
		return settings, err
	}
	if settings.RestoreLockTTL <= settings.RestoreRequestTimeout+time.Second {
		return settings, fmt.Errorf("USER_LIKE_RESTORE_LOCK_TTL must exceed USER_LIKE_RESTORE_REQUEST_TIMEOUT by at least 1s")
	}
	return settings, nil
}

func parseBoolSetting(name string, fallback bool) (bool, error) {
	raw, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", name)
	}
}

func parseDurationSetting(name string, fallback time.Duration) (time.Duration, error) {
	raw, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	switch name {
	case "USER_LIKE_SET_TTL":
		if value < minUserLikeSetTTL || value > maxUserLikeSetTTL {
			return 0, fmt.Errorf("%s must be between 1h and 365d", name)
		}
	case "USER_LIKE_RESTORE_LOCK_TTL":
		if value < time.Second || value > 5*time.Minute {
			return 0, fmt.Errorf("%s must be between 1s and 5m", name)
		}
	case "USER_LIKE_RESTORE_REQUEST_TIMEOUT":
		if value < 100*time.Millisecond || value > 30*time.Second {
			return 0, fmt.Errorf("%s must be between 100ms and 30s", name)
		}
	}
	if value.Milliseconds() <= 0 {
		return 0, fmt.Errorf("%s is too small for Redis millisecond precision", name)
	}
	return value, nil
}

func parseBoundedIntSetting(name string, fallback, minimum, maximum int) (int, error) {
	raw, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}
