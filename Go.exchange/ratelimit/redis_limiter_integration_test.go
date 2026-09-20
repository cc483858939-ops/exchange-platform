package ratelimit

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func openRateLimitTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("SKIPPED — REDIS_TEST_ADDR unavailable")
	}
	database, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{
		Addr:     address,
		DB:       database,
		Password: os.Getenv("REDIS_TEST_PASSWORD"),
	})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Skipf("SKIPPED — Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedisLimiterAtomicMultiWindowAndTTLIntegration(t *testing.T) {
	client := openRateLimitTestRedis(t)
	subject := fmt.Sprintf("rate-limit-test-%d", time.Now().UnixNano())
	now := time.Unix(1_700_000_012, 250_000_000).UTC()
	limiter := &RedisLimiter{client: client, now: func() time.Time { return now }}
	policy, err := PolicyFor(ActionPostCreate)
	if err != nil {
		t.Fatal(err)
	}
	keys := rateLimitKeysForPolicy(subject, ActionPostCreate, policy, now)
	t.Cleanup(func() {
		_ = client.Del(keys...).Err()
	})

	first, err := limiter.Allow(context.Background(), Input{Subject: subject, Action: ActionPostCreate})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Allowed || first.Limit != 5 || first.Remaining != 4 {
		t.Fatalf("unexpected first decision: %#v", first)
	}
	for _, key := range keys {
		count, err := client.Get(key).Int64()
		if err != nil {
			t.Fatalf("GET %s: %v", key, err)
		}
		if count != 1 {
			t.Fatalf("key %s count=%d, want 1", key, count)
		}
		if ttl, err := client.TTL(key).Result(); err != nil || ttl <= 0 {
			t.Fatalf("key %s TTL=%s err=%v, want positive TTL", key, ttl, err)
		}
	}

	for index := 0; index < 4; index++ {
		if decision, err := limiter.Allow(context.Background(), Input{Subject: subject, Action: ActionPostCreate}); err != nil || !decision.Allowed {
			t.Fatalf("allowance %d decision=%#v err=%v", index, decision, err)
		}
	}
	denied, err := limiter.Allow(context.Background(), Input{Subject: subject, Action: ActionPostCreate})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.Remaining != 0 || denied.Limit != 5 || denied.RetryAfter <= 0 {
		t.Fatalf("unexpected denied decision: %#v", denied)
	}
	for _, key := range keys {
		count, err := client.Get(key).Int64()
		if err != nil {
			t.Fatalf("GET after denial %s: %v", key, err)
		}
		if count != 5 {
			t.Fatalf("key %s count=%d after denial, want 5", key, count)
		}
	}
}

func TestRedisLimiterDoesNotPartiallyIncrementBlockedRequestIntegration(t *testing.T) {
	client := openRateLimitTestRedis(t)
	subject := fmt.Sprintf("rate-limit-partial-%d", time.Now().UnixNano())
	now := time.Unix(1_700_000_012, 0).UTC()
	limiter := &RedisLimiter{client: client, now: func() time.Time { return now }}
	policy, err := PolicyFor(ActionPostCreate)
	if err != nil {
		t.Fatal(err)
	}
	keys := rateLimitKeysForPolicy(subject, ActionPostCreate, policy, now)
	t.Cleanup(func() { _ = client.Del(keys...).Err() })
	if err := client.Set(keys[0], 5, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(keys[1], 20, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(keys[2], 20, 0).Err(); err != nil {
		t.Fatal(err)
	}

	decision, err := limiter.Allow(context.Background(), Input{Subject: subject, Action: ActionPostCreate})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("blocked minute window was allowed")
	}
	for index, want := range []int64{5, 20, 20} {
		got, err := client.Get(keys[index]).Int64()
		if err != nil {
			t.Fatalf("GET %s: %v", keys[index], err)
		}
		if got != want {
			t.Fatalf("key %s count=%d after blocked request, want %d", keys[index], got, want)
		}
	}
}

func TestRedisLimiterConcurrentCallsEnforceSingleWindowIntegration(t *testing.T) {
	client := openRateLimitTestRedis(t)
	action := Action("test_concurrency")
	withTestPolicy(t, action, Policy{Action: action, Rules: []Rule{{Limit: 5, Window: time.Minute}}})
	subject := fmt.Sprintf("rate-limit-concurrent-%d", time.Now().UnixNano())
	now := time.Unix(1_700_000_012, 0).UTC()
	limiter := &RedisLimiter{client: client, now: func() time.Time { return now }}
	key := rateLimitKey(subject, action, 60, now.Unix()/60)
	t.Cleanup(func() { _ = client.Del(key).Err() })

	const calls = 100
	decisions := make(chan Decision, calls)
	errors := make(chan error, calls)
	var group sync.WaitGroup
	for index := 0; index < calls; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			decision, err := limiter.Allow(context.Background(), Input{Subject: subject, Action: action})
			if err != nil {
				errors <- err
				return
			}
			decisions <- decision
		}()
	}
	group.Wait()
	close(decisions)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	allowed := 0
	denied := 0
	for decision := range decisions {
		if decision.Allowed {
			allowed++
		} else {
			denied++
		}
	}
	if allowed != 5 || denied != 95 {
		t.Fatalf("allowed=%d denied=%d, want 5/95", allowed, denied)
	}
}

func rateLimitKeysForPolicy(subject string, action Action, policy Policy, now time.Time) []string {
	keys := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		windowSeconds := int64(rule.Window / time.Second)
		keys = append(keys, rateLimitKey(subject, action, windowSeconds, now.Unix()/windowSeconds))
	}
	return keys
}
