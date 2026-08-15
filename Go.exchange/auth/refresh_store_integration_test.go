package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestRedisRefreshRotationAllowsExactlyOneConcurrentWinnerIntegration(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if address == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	database := 0
	if raw := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		database = parsed
	}
	client := redis.NewClient(&redis.Options{
		Addr:     address,
		Password: os.Getenv("REDIS_TEST_PASSWORD"),
		DB:       database,
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisRefreshStore(client)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		ActiveKID:          "redis-test-v1",
		PrivateKey:         privateKey,
		VerifyKeys:         map[string]ed25519.PublicKey{"redis-test-v1": publicKey},
		Issuer:             "go.exchange.test",
		Audience:           "go.exchange.test.api",
		AccessTTL:          15 * time.Minute,
		RefreshIdleTTL:     time.Hour,
		RefreshAbsoluteTTL: 24 * time.Hour,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := manager.IssuePair(context.Background(), 314)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := refreshSessionKey(parsed.sessionID)
	usedKey := usedRefreshKey(parsed.sessionID, hashRefreshSecret(parsed.secret))
	t.Cleanup(func() { _ = client.Del(sessionKey, usedKey).Err() })

	stored, err := client.HGetAll(sessionKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if stored["secret_hash"] != hashRefreshSecret(parsed.secret) {
		t.Fatalf("stored hash=%q", stored["secret_hash"])
	}
	if stored["secret_hash"] == pair.RefreshToken || stored["secret_hash"] == string(parsed.secret) {
		t.Fatal("Redis stored refresh token plaintext")
	}

	const workers = 100
	var successes atomic.Int64
	var expectedFailures atomic.Int64
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, rotateErr := manager.RotateRefresh(context.Background(), pair.RefreshToken)
			if rotateErr == nil {
				successes.Add(1)
				return
			}
			if !errors.Is(rotateErr, ErrRefreshReused) {
				t.Errorf("unexpected rotation error: %v", rotateErr)
				return
			}
			expectedFailures.Add(1)
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || expectedFailures.Load() != workers-1 {
		t.Fatalf("successes=%d expected_failures=%d", successes.Load(), expectedFailures.Load())
	}
	if exists, err := client.Exists(sessionKey).Result(); err != nil || exists != 0 {
		t.Fatalf("replayed token must revoke the session: exists=%d err=%v", exists, err)
	}
}
