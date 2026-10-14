package controllers

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

func TestPostDetailCacheFenceScriptsIntegration(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if address == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	dbNumber, err := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	if err != nil && os.Getenv("REDIS_TEST_DB") != "" {
		t.Fatalf("parse REDIS_TEST_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatalf("connect to Redis test service: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client2 := redis.NewClient(&redis.Options{Addr: address, DB: dbNumber})
	if err := client2.Ping().Err(); err != nil {
		client2.Close()
		t.Fatalf("connect second Redis test client: %v", err)
	}
	t.Cleanup(func() { _ = client2.Close() })

	id := "fence-test-" + uuid.NewString()
	cacheKey := postDetailCacheKey(id)
	fenceKey := postDetailFenceKey(id)
	initID := id + "-init"
	initFenceKey := postDetailFenceKey(initID)
	t.Cleanup(func() {
		if err := client.Del(cacheKey, fenceKey, initFenceKey).Err(); err != nil {
			t.Errorf("clean Post detail fence test keys: %v", err)
		}
	})
	ctx := context.Background()

	// Two independent Redis clients initialize the same missing fence at once.
	startInitialization := make(chan struct{})
	type initializationResult struct {
		token string
		err   error
	}
	initializationResults := make(chan initializationResult, 2)
	for _, redisClient := range []*redis.Client{client, client2} {
		go func(redisClient *redis.Client) {
			<-startInitialization
			token, err := getOrCreatePostDetailFenceToken(ctx, redisClient, initID)
			initializationResults <- initializationResult{token: token, err: err}
		}(redisClient)
	}
	close(startInitialization)
	firstInitialization := <-initializationResults
	secondInitialization := <-initializationResults
	if firstInitialization.err != nil || secondInitialization.err != nil {
		t.Fatalf("concurrent fence initialization errors: first=%v second=%v", firstInitialization.err, secondInitialization.err)
	}
	if firstInitialization.token != secondInitialization.token {
		t.Fatalf("concurrent initialization returned different tokens: %q and %q", firstInitialization.token, secondInitialization.token)
	}
	initializationTTL, err := client.PTTL(initFenceKey).Result()
	if err != nil || initializationTTL <= 0 || initializationTTL > postDetailFenceTTL {
		t.Fatalf("initialized fence TTL=%s err=%v want (0,%s]", initializationTTL, err, postDetailFenceTTL)
	}

	getter := func(key string) (string, error) { return client.Get(key).Result() }
	fenceGetter := func() (string, error) { return getOrCreatePostDetailFenceToken(ctx, client, id) }
	setter := func(token string, payload []byte, expiration time.Duration) (bool, error) {
		return setPostDetailCacheIfFenceMatches(ctx, client, id, token, payload, expiration)
	}
	postCacheGroup = singleflight.Group{}

	firstFenceCaptured := make(chan string, 1)
	oldSnapshotReady := make(chan struct{})
	allowOldBackfill := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(allowOldBackfill) })
	var loads atomic.Int32
	loader := func() (cacheTestPayload, error) {
		if loads.Add(1) == 1 {
			close(oldSnapshotReady)
			<-allowOldBackfill
			return cacheTestPayload{Value: "stale"}, nil
		}
		return cacheTestPayload{Value: "fresh"}, nil
	}
	fencedGetter := func() (string, error) {
		token, err := fenceGetter()
		if err == nil {
			select {
			case firstFenceCaptured <- token:
			default:
			}
		}
		return token, err
	}
	type loadResult struct {
		value cacheTestPayload
		err   error
	}
	resultCh := make(chan loadResult, 1)
	go func() {
		value, err := loadJSONCacheWithFenceStoreContext(ctx, cacheKey, postCacheTTL, getter, fencedGetter, setter, loader)
		resultCh <- loadResult{value: value, err: err}
	}()

	var tokenA string
	select {
	case tokenA = <-firstFenceCaptured:
	case result := <-resultCh:
		t.Fatalf("load exited before capturing its fence: value=%+v err=%v", result.value, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the initial fence token")
	}
	select {
	case <-oldSnapshotReady:
	case result := <-resultCh:
		t.Fatalf("load exited before returning the stale snapshot: value=%+v err=%v", result.value, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the stale snapshot")
	}
	if err := invalidatePostDetailCacheKeyWithRedis(client, cacheKey); err != nil {
		t.Fatalf("invalidate detail cache: %v", err)
	}
	tokenB, err := getOrCreatePostDetailFenceToken(ctx, client, id)
	if err != nil {
		t.Fatal(err)
	}
	if tokenA == tokenB {
		t.Fatalf("invalidation reused fence token %q", tokenA)
	}
	if err := client.Get(cacheKey).Err(); err != redis.Nil {
		t.Fatalf("cache should be absent after invalidation, got err=%v", err)
	}
	releaseOnce.Do(func() { close(allowOldBackfill) })

	var result loadResult
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the bounded fresh reload")
	}
	if result.err != nil || result.value.Value != "fresh" {
		t.Fatalf("post-invalidation load=%+v err=%v want fresh", result.value, result.err)
	}
	if loads.Load() != 2 {
		t.Fatalf("loader calls=%d want 2 (stale attempt plus one fresh retry)", loads.Load())
	}
	cached, err := client.Get(cacheKey).Result()
	if err != nil || cached != `{"value":"fresh"}` {
		t.Fatalf("cache after old backfill=%q err=%v want fresh payload", cached, err)
	}
	fenceTTL, err := client.PTTL(fenceKey).Result()
	if err != nil || fenceTTL <= 0 || fenceTTL > postDetailFenceTTL {
		t.Fatalf("fence TTL=%s err=%v want (0,%s]", fenceTTL, err, postDetailFenceTTL)
	}
	cacheTTL, err := client.PTTL(cacheKey).Result()
	if err != nil || cacheTTL <= 0 || cacheTTL > postCacheTTL {
		t.Fatalf("cache TTL=%s err=%v want (0,%s]", cacheTTL, err, postCacheTTL)
	}

	// A cache write before invalidation is removed by the atomic invalidator.
	oldPayload := []byte(`{"value":"old-before-invalidation"}`)
	if written, err := setter(tokenB, oldPayload, postCacheTTL); err != nil || !written {
		t.Fatalf("write before invalidation written=%t err=%v", written, err)
	}
	if err := invalidatePostDetailCacheKeyWithRedis(client, cacheKey); err != nil {
		t.Fatalf("second invalidation: %v", err)
	}
	tokenC, err := getOrCreatePostDetailFenceToken(ctx, client, id)
	if err != nil {
		t.Fatal(err)
	}
	if tokenC == tokenB {
		t.Fatalf("second invalidation reused fence token %q", tokenB)
	}
	if err := client.Get(cacheKey).Err(); err != redis.Nil {
		t.Fatalf("invalidation did not delete the prior cache value: %v", err)
	}
	if written, err := setter(tokenB, oldPayload, postCacheTTL); err != nil || written {
		t.Fatalf("old post-invalidation write written=%t err=%v want rejected", written, err)
	}
	if err := client.Get(cacheKey).Err(); err != redis.Nil {
		t.Fatalf("rejected old write left a cache value: %v", err)
	}
	if written, err := setter(tokenC, []byte(`{"value":"new"}`), postCacheTTL); err != nil || !written {
		t.Fatalf("current-token write written=%t err=%v", written, err)
	}

	// Expiring a token and initializing again must produce a distinct fence.
	if err := client.PExpire(fenceKey, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		exists, err := client.Exists(fenceKey).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired fence key still exists after 2 seconds")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tokenD, err := getOrCreatePostDetailFenceToken(ctx, client, id)
	if err != nil {
		t.Fatal(err)
	}
	if tokenD == tokenC {
		t.Fatalf("expired token was reused: %q", tokenD)
	}
	if written, err := setter(tokenC, oldPayload, postCacheTTL); err != nil || written {
		t.Fatalf("expired-token write written=%t err=%v want rejected", written, err)
	}
	if written, err := setter(tokenD, []byte(`{"value":"after-expiry"}`), postCacheTTL); err != nil || !written {
		t.Fatalf("new-token write written=%t err=%v", written, err)
	}
	if value, err := client.Get(cacheKey).Result(); err != nil || value != `{"value":"after-expiry"}` {
		t.Fatalf("cache after token expiry=%q err=%v", value, err)
	}
}
