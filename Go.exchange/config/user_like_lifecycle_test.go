package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestUserLikeLifecycleSettingsDefaultsAndIndependentSwitches(t *testing.T) {
	unsetUserLikeLifecycleEnvironment(t)
	settings, err := UserLikeLifecycleSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ArmingEnabled || !settings.RestoreEnabled || settings.SetTTL != 72*time.Hour ||
		settings.RestoreLockTTL != 30*time.Second || settings.RestoreBatchSize != 500 ||
		settings.RestoreMaxRelations != 10000 || settings.RestoreRequestTimeout != 2*time.Second ||
		settings.RestoreConcurrency != 8 {
		t.Fatalf("unexpected defaults: %+v", settings)
	}

	t.Setenv("USER_LIKE_TTL_ARMING_ENABLED", "false")
	t.Setenv("USER_LIKE_TTL_RESTORE_ENABLED", "false")
	settings, err = UserLikeLifecycleSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ArmingEnabled || settings.RestoreEnabled {
		t.Fatalf("arming and restore switches were not independent: %+v", settings)
	}
}

func TestUserLikeLifecycleSettingsRejectUnsafeValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "short TTL", key: "USER_LIKE_SET_TTL", value: "1s", want: "between 1h and 365d"},
		{name: "duration overflow", key: "USER_LIKE_SET_TTL", value: "999999999999999999999999h", want: "positive duration"},
		{name: "invalid arming bool", key: "USER_LIKE_TTL_ARMING_ENABLED", value: "sometimes", want: "must be a boolean"},
		{name: "unbounded batch", key: "USER_LIKE_RESTORE_BATCH_SIZE", value: "5000000", want: "must be between"},
		{name: "unbounded relation count", key: "USER_LIKE_RESTORE_MAX_RELATIONS", value: "0", want: "must be between"},
		{name: "relation count above hard cap", key: "USER_LIKE_RESTORE_MAX_RELATIONS", value: "10001", want: "must be between"},
		{name: "lock shorter than request", key: "USER_LIKE_RESTORE_LOCK_TTL", value: "2s", want: "must exceed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			unsetUserLikeLifecycleEnvironment(t)
			t.Setenv(test.key, test.value)
			if _, err := UserLikeLifecycleSettings(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("settings err=%v, want substring %q", err, test.want)
			}
		})
	}
}

func unsetUserLikeLifecycleEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"USER_LIKE_TTL_ARMING_ENABLED", "USER_LIKE_TTL_RESTORE_ENABLED", "USER_LIKE_SET_TTL",
		"USER_LIKE_RESTORE_LOCK_TTL", "USER_LIKE_RESTORE_BATCH_SIZE", "USER_LIKE_RESTORE_MAX_RELATIONS",
		"USER_LIKE_RESTORE_REQUEST_TIMEOUT", "USER_LIKE_RESTORE_CONCURRENCY",
	} {
		value, existed := "", false
		if old, ok := os.LookupEnv(key); ok {
			value, existed = old, true
		}
		_ = os.Unsetenv(key)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
