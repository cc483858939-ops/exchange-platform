package controllers

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func openRecommendationClickCooldownIntegrationRedis(t *testing.T) *redis.Client {
	t.Helper()
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
	return client
}

func integrationCooldownIdentity() (uint, uint) {
	userID := uint(uint64(time.Now().UnixNano()) & 0x7fffffff)
	if userID == 0 {
		userID = 1
	}
	return userID, userID + 1
}

func TestRecommendationClickCooldownFiveMinutesIntegration(t *testing.T) {
	client := openRecommendationClickCooldownIntegrationRedis(t)
	userID, postID := integrationCooldownIdentity()
	key := recommendationClickCooldownKey(userID, postID, "request-a")
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation click cooldown test key: %v", err)
		}
	})
	store, err := newRedisRecommendationClickCooldown(client, 60*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Acquire(context.Background(), userID, postID, "request-a", "event-a")
	if err != nil || first != RecommendationClickCooldownAcquired {
		t.Fatalf("first acquire=%v err=%v", first, err)
	}
	if err := store.Commit(context.Background(), userID, postID, "request-a", "event-a"); err != nil {
		t.Fatal(err)
	}
	initialTTL, err := client.TTL(key).Result()
	if err != nil || initialTTL < 299*time.Second || initialTTL > 300*time.Second {
		t.Fatalf("committed TTL=%s err=%v want about 300s", initialTTL, err)
	}
	second, err := store.Acquire(context.Background(), userID, postID, "request-a", "event-b")
	if err != nil || second != RecommendationClickCooldownDuplicate {
		t.Fatalf("second acquire=%v err=%v want committed duplicate", second, err)
	}
	remainingTTL, err := client.TTL(key).Result()
	if err != nil || remainingTTL <= 0 || remainingTTL > initialTTL {
		t.Fatalf("duplicate refreshed/removed cooldown TTL: before=%s after=%s err=%v", initialTTL, remainingTTL, err)
	}

	// Use a short test lease to prove expiration opens a new click without
	// waiting five minutes in the integration suite; the production TTL above
	// is checked against the exact 300-second setting.
	if err := client.Del(key).Err(); err != nil {
		t.Fatal(err)
	}
	shortStore, err := newRedisRecommendationClickCooldown(client, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if acquired, err := shortStore.Acquire(context.Background(), userID, postID, "request-a", "event-c"); err != nil || acquired != RecommendationClickCooldownAcquired {
		t.Fatalf("short lease first acquire=%v err=%v", acquired, err)
	}
	if err := shortStore.Commit(context.Background(), userID, postID, "request-a", "event-c"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if acquired, err := shortStore.Acquire(context.Background(), userID, postID, "request-a", "event-d"); err != nil || acquired != RecommendationClickCooldownAcquired {
		t.Fatalf("acquire after expiry=%v err=%v", acquired, err)
	}
}

func TestRecommendationClickCooldownRedisScopesUserAndPostIntegration(t *testing.T) {
	client := openRecommendationClickCooldownIntegrationRedis(t)
	store, err := newRedisRecommendationClickCooldown(client, 60*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	userID, postID := integrationCooldownIdentity()
	keys := []string{
		recommendationClickCooldownKey(userID, postID, "request-a"),
		recommendationClickCooldownKey(userID+1, postID, "request-a"),
		recommendationClickCooldownKey(userID, postID+1, "request-a"),
	}
	t.Cleanup(func() {
		if err := client.Del(keys...).Err(); err != nil {
			t.Errorf("delete recommendation click cooldown scope test keys: %v", err)
		}
	})
	for _, test := range []struct {
		userID  uint
		postID  uint
		eventID string
	}{{userID, postID, "event-a"}, {userID + 1, postID, "event-b"}, {userID, postID + 1, "event-c"}} {
		result, err := store.Acquire(context.Background(), test.userID, test.postID, "request-a", test.eventID)
		if err != nil || result != RecommendationClickCooldownAcquired {
			t.Fatalf("user=%d post=%d acquire=%v err=%v", test.userID, test.postID, result, err)
		}
	}
}

func TestRecommendationClickCooldownConcurrentAcquireIntegration(t *testing.T) {
	firstClient := openRecommendationClickCooldownIntegrationRedis(t)
	secondClient := openRecommendationClickCooldownIntegrationRedis(t)
	userID, postID := integrationCooldownIdentity()
	key := recommendationClickCooldownKey(userID, postID, "request-a")
	t.Cleanup(func() {
		if err := firstClient.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation click cooldown concurrency key: %v", err)
		}
	})
	firstStore, err := newRedisRecommendationClickCooldown(firstClient, 60*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := newRedisRecommendationClickCooldown(secondClient, 60*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	type acquireOutcome struct {
		eventID string
		result  RecommendationClickCooldownAcquireResult
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan acquireOutcome, 2)
	var wait sync.WaitGroup
	for _, candidate := range []struct {
		store   RecommendationClickCooldown
		eventID string
	}{{firstStore, "event-a"}, {secondStore, "event-b"}} {
		wait.Add(1)
		go func(store RecommendationClickCooldown, eventID string) {
			defer wait.Done()
			<-start
			result, err := store.Acquire(context.Background(), userID, postID, "request-a", eventID)
			outcomes <- acquireOutcome{eventID: eventID, result: result, err: err}
		}(candidate.store, candidate.eventID)
	}
	close(start)
	wait.Wait()
	close(outcomes)
	var owner string
	acquired, pending := 0, 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		switch outcome.result {
		case RecommendationClickCooldownAcquired:
			acquired++
			owner = outcome.eventID
		case RecommendationClickCooldownPending:
			pending++
		default:
			t.Fatalf("concurrent acquire result=%v want acquired or pending", outcome.result)
		}
	}
	if acquired != 1 || pending != 1 {
		t.Fatalf("concurrent outcomes acquired=%d pending=%d want 1 each", acquired, pending)
	}
	if err := firstStore.Commit(context.Background(), userID, postID, "request-a", owner); err != nil {
		t.Fatal(err)
	}
	result, err := secondStore.Acquire(context.Background(), userID, postID, "request-a", "event-c")
	if err != nil || result != RecommendationClickCooldownDuplicate {
		t.Fatalf("post-commit acquire=%v err=%v want duplicate", result, err)
	}
}

func TestRecommendationClickCooldownReleaseCannotDeleteAnotherOwnerIntegration(t *testing.T) {
	client := openRecommendationClickCooldownIntegrationRedis(t)
	userID, postID := integrationCooldownIdentity()
	key := recommendationClickCooldownKey(userID, postID, "request-a")
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation click cooldown ownership key: %v", err)
		}
	})
	store, err := newRedisRecommendationClickCooldown(client, time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.Acquire(context.Background(), userID, postID, "request-a", "event-a"); err != nil || result != RecommendationClickCooldownAcquired {
		t.Fatalf("owner A acquire=%v err=%v", result, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if result, err := store.Acquire(context.Background(), userID, postID, "request-a", "event-b"); err != nil || result != RecommendationClickCooldownAcquired {
		t.Fatalf("owner B acquire=%v err=%v", result, err)
	}
	if err := store.Release(context.Background(), userID, postID, "request-a", "event-a"); err != nil {
		t.Fatal(err)
	}
	value, err := client.Get(key).Result()
	if err != nil || value != "PENDING:event-b" {
		t.Fatalf("key after delayed release=%q err=%v want B reservation", value, err)
	}
	if err := store.Commit(context.Background(), userID, postID, "request-a", "event-a"); err != ErrRecommendationClickCooldownOwnershipLost {
		t.Fatalf("old owner commit error=%v want ownership lost", err)
	}
	if err := store.Commit(context.Background(), userID, postID, "request-a", "event-b"); err != nil {
		t.Fatalf("current owner commit: %v", err)
	}
}

func TestRecommendationClickCooldownSeparatesRequestsIntegration(t *testing.T) {
	client := openRecommendationClickCooldownIntegrationRedis(t)
	userID, postID := integrationCooldownIdentity()
	requestA, requestB := "opportunity-a", "opportunity-b"
	keys := []string{
		recommendationClickCooldownKey(userID, postID, requestA),
		recommendationClickCooldownKey(userID, postID, requestB),
	}
	t.Cleanup(func() { _ = client.Del(keys...).Err() })
	store, err := newRedisRecommendationClickCooldown(client, 60*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := store.Acquire(context.Background(), userID, postID, requestA, "click-a"); err != nil || outcome != RecommendationClickCooldownAcquired {
		t.Fatalf("request A acquire=%v err=%v", outcome, err)
	}
	if err := store.Commit(context.Background(), userID, postID, requestA, "click-a"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := store.Acquire(context.Background(), userID, postID, requestB, "click-b"); err != nil || outcome != RecommendationClickCooldownAcquired {
		t.Fatalf("new recommendation request blocked: result=%v err=%v", outcome, err)
	}
}
