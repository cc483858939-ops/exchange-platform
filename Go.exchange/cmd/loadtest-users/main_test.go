package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func lookupEnvironment(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoadTestUserConfigRequiresPassword(t *testing.T) {
	if _, err := loadTestUserConfigFromEnv(lookupEnvironment(map[string]string{})); err == nil || !strings.Contains(err.Error(), "LOADTEST_USER_PASSWORD") {
		t.Fatalf("missing password error=%v", err)
	}
}

func TestLoadTestUserConfigDefaults(t *testing.T) {
	got, err := loadTestUserConfigFromEnv(lookupEnvironment(map[string]string{
		"LOADTEST_USER_PASSWORD": "local-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "local-secret" || got.Count != defaultLoadTestUserCount || got.Prefix != defaultLoadTestUserPrefix {
		t.Fatalf("config=%+v", got)
	}
}

func TestLoadTestUserConfigRejectsInvalidCounts(t *testing.T) {
	for _, raw := range []string{"0", "21", "not-a-number"} {
		_, err := loadTestUserConfigFromEnv(lookupEnvironment(map[string]string{
			"LOADTEST_USER_PASSWORD": "secret",
			"LOADTEST_USER_COUNT":    raw,
		}))
		if err == nil {
			t.Fatalf("count %q was accepted", raw)
		}
	}
}

func TestLoadTestUserConfigAcceptsCountBoundaries(t *testing.T) {
	for _, raw := range []string{"1", "20"} {
		got, err := loadTestUserConfigFromEnv(lookupEnvironment(map[string]string{
			"LOADTEST_USER_PASSWORD": "secret",
			"LOADTEST_USER_COUNT":    raw,
		}))
		if err != nil {
			t.Fatalf("count %q error=%v", raw, err)
		}
		if got.Count < minLoadTestUserCount || got.Count > maxLoadTestUserCount {
			t.Fatalf("count=%d", got.Count)
		}
	}
}

func TestLoadTestUserConfigValidatesPrefix(t *testing.T) {
	for _, prefix := range []string{"loadtestv1", "loadtest_local", "loadtest-baseline", "loadtest"} {
		if err := validateLoadTestUserPrefix(prefix); err != nil {
			t.Fatalf("valid prefix %q error=%v", prefix, err)
		}
	}
	for _, prefix := range []string{"", "admin", "user", "production", "loadtest local", "loadtest/unsafe", "loadtest-用户"} {
		if err := validateLoadTestUserPrefix(prefix); err == nil {
			t.Fatalf("invalid prefix %q was accepted", prefix)
		}
	}
}

func TestSyntheticUserNamesAreDeterministic(t *testing.T) {
	if got, want := syntheticUserUsername("loadtestv1", 1), "loadtestv1_0001"; got != want {
		t.Fatalf("username=%q want=%q", got, want)
	}
	if got, want := syntheticUserUsername("loadtestv1", 10), "loadtestv1_0010"; got != want {
		t.Fatalf("username=%q want=%q", got, want)
	}
	if got, want := syntheticUserDisplayName(10), "Load Test User 0010"; got != want {
		t.Fatalf("display name=%q want=%q", got, want)
	}
}

func TestProvisionSyntheticUsersIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}

	prefix := "loadtest-command-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	userConfig := loadTestUserConfig{Password: "integration-secret", Count: 2, Prefix: prefix}
	targetUsernames := []string{syntheticUserUsername(prefix, 1), syntheticUserUsername(prefix, 2)}
	ordinaryUsername := "ordinary-loadtest-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		_ = db.Unscoped().Where("username IN ?", append(targetUsernames, ordinaryUsername)).Delete(&models.User{}).Error
	})

	first, err := provisionSyntheticUsers(context.Background(), db, userConfig)
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 2 || first.Updated != 0 || first.Restored != 0 {
		t.Fatalf("first report=%+v", first)
	}

	second, err := provisionSyntheticUsers(context.Background(), db, userConfig)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created != 0 || second.Updated != 2 || second.Restored != 0 {
		t.Fatalf("second report=%+v", second)
	}

	var deleted models.User
	if err := db.Where("username = ?", targetUsernames[0]).First(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}

	ordinaryPasswordHash, err := utils.HashPassword("ordinary-old")
	if err != nil {
		t.Fatal(err)
	}
	ordinary := models.User{
		Username:    ordinaryUsername,
		Password:    ordinaryPasswordHash,
		DisplayName: "Do not touch",
	}
	if err := db.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}

	third, err := provisionSyntheticUsers(context.Background(), db, userConfig)
	if err != nil {
		t.Fatal(err)
	}
	if third.Created != 0 || third.Updated != 1 || third.Restored != 1 {
		t.Fatalf("third report=%+v", third)
	}

	var restored models.User
	if err := db.Where("username = ?", targetUsernames[0]).First(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if restored.DeletedAt.Valid || restored.DisplayName != "Load Test User 0001" || !utils.CheckPassword(userConfig.Password, restored.Password) {
		t.Fatalf("restored user=%+v", restored)
	}

	var unchanged models.User
	if err := db.Where("username = ?", ordinaryUsername).First(&unchanged).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.DisplayName != ordinary.DisplayName ||
		unchanged.Password != ordinary.Password ||
		!utils.CheckPassword("ordinary-old", unchanged.Password) {
		t.Fatalf("ordinary user was changed=%+v", unchanged)
	}
}
