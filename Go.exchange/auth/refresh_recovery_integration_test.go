package auth

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func redisRecoveryFixture(t *testing.T) (*Manager, *redis.Client, TokenPair, string, string) {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if address == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	database := 0
	if raw := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); raw != "" {
		var err error
		database, err = strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: database, Password: os.Getenv("REDIS_TEST_PASSWORD")})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisRefreshStore(client)
	if err != nil {
		t.Fatal(err)
	}
	template, _, _ := testManager(t)
	manager, err := NewManager(template.config, store)
	if err != nil {
		t.Fatal(err)
	}
	original, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := parseRefreshToken(original.RefreshToken)
	// Only random sessions created by this fixture are removed.
	t.Cleanup(func() {
		keys, err := client.Keys("auth:refresh:*{" + parsed.sessionID + "}*").Result()
		if err != nil {
			t.Error(err)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(keys...).Err(); err != nil {
				t.Error(err)
			}
		}
	})
	return manager, client, original, parsed.sessionID, hashRefreshSecret(parsed.secret)
}

func TestRedisRefreshRecoverySameRequestIntegration(t *testing.T) {
	manager, client, original, sessionID, expectedHash := redisRecoveryFixture(t)
	requestID := uuid.NewString()
	first, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
	if err != nil {
		t.Fatal(err)
	}
	key := refreshRecoveryKey(sessionID, expectedHash, requestID)
	if ttl := client.TTL(key).Val(); ttl <= 0 || ttl > refreshRecoveryTTL {
		t.Fatalf("recovery ttl=%s", ttl)
	}
	stored := client.HGetAll(key).Val()
	if stored["sealed_secret"] == "" || strings.Contains(stored["sealed_secret"], first.RefreshToken) {
		t.Fatal("recovery secret is not sealed")
	}
	if err := client.Expire(key, 15*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Expire(refreshSessionKey(sessionID), 25*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewManager(manager.config, manager.store)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.RotateRefresh(context.Background(), original.RefreshToken, requestID)
	if err != nil || recovered.RefreshToken != first.RefreshToken {
		t.Fatalf("recovery=%v", err)
	}
	if recovered.RefreshExpiresIn > 25*time.Second || client.TTL(key).Val() > 15*time.Second {
		t.Fatal("retry extended a recovery or session lifetime")
	}
	parsed, _ := parseRefreshToken(first.RefreshToken)
	if client.HGet(refreshSessionKey(sessionID), "secret_hash").Val() != hashRefreshSecret(parsed.secret) {
		t.Fatal("retry changed the current secret hash")
	}
}

func TestRedisRefreshRecoveryConcurrentRequestIntegration(t *testing.T) {
	manager, _, original, _, _ := redisRecoveryFixture(t)
	requestID := uuid.NewString()
	const callers = 20
	var wait sync.WaitGroup
	results := make(chan TokenPair, callers)
	failures := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			pair, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
			if err != nil {
				failures <- err
			} else {
				results <- pair
			}
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Errorf("rotation=%v", err)
	}
	winner := ""
	count := 0
	for pair := range results {
		count++
		if winner == "" {
			winner = pair.RefreshToken
		}
		if winner != pair.RefreshToken {
			t.Fatal("different successors for one request")
		}
	}
	if count != callers {
		t.Fatalf("successful rotations=%d", count)
	}
}

func TestRedisRefreshRecoveryReplayBoundariesIntegration(t *testing.T) {
	for _, scenario := range []string{"different request", "missing request", "expired result", "newer generation", "session removed"} {
		t.Run(scenario, func(t *testing.T) {
			manager, client, original, sessionID, expectedHash := redisRecoveryFixture(t)
			requestID := uuid.NewString()
			next, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "different request":
				requestID = uuid.NewString()
			case "missing request":
				requestID = ""
			case "expired result":
				key := refreshRecoveryKey(sessionID, expectedHash, requestID)
				if err := client.PExpire(key, time.Millisecond).Err(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			case "newer generation":
				if _, err := manager.RotateRefresh(context.Background(), next.RefreshToken, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "session removed":
				if err := client.Del(refreshSessionKey(sessionID)).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID); !errors.Is(err, ErrRefreshReused) {
				t.Fatalf("replay=%v", err)
			}
			if client.Exists(refreshSessionKey(sessionID)).Val() != 0 {
				t.Fatal("replay left refresh session alive")
			}
		})
	}
}
