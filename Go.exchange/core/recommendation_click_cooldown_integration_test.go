package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/controllers"

	"github.com/go-redis/redis/v7"
)

func TestAPIRecommendationClickCooldownUsesFixedFiveMinuteTTLIntegration(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis click cooldown integration test")
	}
	dbNumber := 0
	if rawDB := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); rawDB != "" {
		parsed, err := strconv.Atoi(rawDB)
		if err != nil || parsed < 0 {
			t.Fatalf("invalid REDIS_TEST_DB %q", rawDB)
		}
		dbNumber = parsed
	}
	client := redis.NewClient(&redis.Options{
		Addr: addr, Password: os.Getenv("REDIS_TEST_PASSWORD"), DB: dbNumber,
		DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
	})
	if err := client.Ping().Err(); err != nil {
		t.Fatalf("connect Redis integration test: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis click cooldown integration client: %v", err)
		}
	})

	userID := uint(uint64(time.Now().UnixNano()) & 0x7fffffff)
	if userID == 0 {
		userID = 1
	}
	postID := userID + 1
	key := fmt.Sprintf("recommendation:telemetry:click:cooldown:%d:%s:%d", userID, "request-a", postID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation click cooldown test key: %v", err)
		}
	})

	store, err := newRecommendationClickCooldownStore(client)
	if err != nil {
		t.Fatal(err)
	}
	const eventID = "api-runtime-cooldown-test"
	result, err := store.Acquire(context.Background(), userID, postID, "request-a", eventID)
	if err != nil || result != controllers.RecommendationClickCooldownAcquired {
		t.Fatalf("first acquire=%v err=%v want acquired", result, err)
	}
	pendingTTL, err := client.TTL(key).Result()
	if err != nil || pendingTTL < 59*time.Second || pendingTTL > 60*time.Second {
		t.Fatalf("production pending TTL=%s err=%v want about 60s", pendingTTL, err)
	}
	if err := store.Commit(context.Background(), userID, postID, "request-a", eventID); err != nil {
		t.Fatal(err)
	}
	committedTTL, err := client.TTL(key).Result()
	if err != nil || committedTTL < 299*time.Second || committedTTL > 300*time.Second {
		t.Fatalf("production committed TTL=%s err=%v want about 300s", committedTTL, err)
	}
}
