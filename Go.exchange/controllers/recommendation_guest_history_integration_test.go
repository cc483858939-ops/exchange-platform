package controllers

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestGuestRecommendationServedHistoryRedisIntegration(t *testing.T) {
	client := openGuestRecommendationHistoryIntegrationRedis(t)
	previousRedis := global.RedisDB
	global.RedisDB = client
	t.Cleanup(func() {
		global.RedisDB = previousRedis
	})

	cfg := config.RecommendationConfig{
		ServedHardExclusionMinutes: 30,
		ServedSoftLookbackDays:     7,
		GuestServedHistoryLimit:    3,
	}

	t.Run("write read round trip", func(t *testing.T) {
		sessionID, _ := newGuestRecommendationHistoryIntegrationSession(t, client)
		now := time.Now().UTC().Truncate(time.Second)

		if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{101, 102}, now, cfg); err != nil {
			t.Fatalf("record served history: %v", err)
		}

		history, err := loadGuestRecommendationServedHistory(context.Background(), sessionID, now, cfg)
		if err != nil {
			t.Fatalf("load served history: %v", err)
		}
		for _, postID := range []uint{101, 102} {
			served, ok := history[postID]
			if !ok {
				t.Fatalf("post %d missing from served history: %#v", postID, history)
			}
			if !served.Hard || served.Soft {
				t.Fatalf("post %d classification=%#v want hard only", postID, served)
			}
			if !served.LastServedAt.Equal(now) {
				t.Fatalf("post %d last served at=%s want %s", postID, served.LastServedAt, now)
			}
		}
	})

	t.Run("real Redis classification", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		_, err := client.ZAdd(key,
			&redis.Z{Score: float64(now.Add(-5 * time.Minute).Unix()), Member: "201"},
			&redis.Z{Score: float64(now.Add(-2 * time.Hour).Unix()), Member: "202"},
			&redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "203"},
		).Result()
		if err != nil {
			t.Fatalf("seed served history: %v", err)
		}

		history, err := loadGuestRecommendationServedHistory(context.Background(), sessionID, now, cfg)
		if err != nil {
			t.Fatalf("load served history: %v", err)
		}
		if served := history[201]; !served.Hard || served.Soft {
			t.Fatalf("post 201 classification=%#v want hard only", served)
		}
		if served := history[202]; served.Hard || !served.Soft {
			t.Fatalf("post 202 classification=%#v want soft only", served)
		}
		if _, ok := history[203]; ok {
			t.Fatalf("expired post 203 remained in history: %#v", history[203])
		}
	})

	t.Run("timestamp refresh", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		t0 := time.Now().UTC().Truncate(time.Second)
		t1 := t0.Add(2 * time.Hour)

		if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{301}, t0, cfg); err != nil {
			t.Fatalf("record initial served history: %v", err)
		}
		assertGuestRecommendationHistoryScore(t, client, key, "301", t0)

		if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{301}, t1, cfg); err != nil {
			t.Fatalf("refresh served history: %v", err)
		}
		assertGuestRecommendationHistoryScore(t, client, key, "301", t1)

		history, err := loadGuestRecommendationServedHistory(context.Background(), sessionID, t1, cfg)
		if err != nil {
			t.Fatalf("load refreshed served history: %v", err)
		}
		served, ok := history[301]
		if !ok || !served.Hard || served.Soft || !served.LastServedAt.Equal(t1) {
			t.Fatalf("refreshed post classification=%#v present=%t want hard at %s", served, ok, t1)
		}
	})

	t.Run("expired cleanup", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		_, err := client.ZAdd(key, &redis.Z{
			Score:  float64(now.Add(-8 * 24 * time.Hour).Unix()),
			Member: "401",
		}).Result()
		if err != nil {
			t.Fatalf("seed expired served history: %v", err)
		}

		if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{402}, now, cfg); err != nil {
			t.Fatalf("record served history with expired entry: %v", err)
		}
		if _, err := client.ZScore(key, "401").Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("expired post 401 score error=%v want redis.Nil", err)
		}
		assertGuestRecommendationHistoryScore(t, client, key, "402", now)
	})

	t.Run("history cap", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		t0 := time.Now().UTC().Truncate(time.Second)
		for index, postID := range []uint{501, 502, 503, 504} {
			if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{postID}, t0.Add(time.Duration(index)*time.Second), cfg); err != nil {
				t.Fatalf("record post %d: %v", postID, err)
			}
		}

		cardinality, err := client.ZCard(key).Result()
		if err != nil {
			t.Fatalf("read history cardinality: %v", err)
		}
		if cardinality != 3 {
			t.Fatalf("history cardinality=%d want 3", cardinality)
		}
		if _, err := client.ZScore(key, "501").Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("oldest post 501 score error=%v want redis.Nil", err)
		}
		for _, postID := range []string{"502", "503", "504"} {
			if _, err := client.ZScore(key, postID).Result(); err != nil {
				t.Fatalf("post %s was removed from capped history: %v", postID, err)
			}
		}
	})

	t.Run("bounded read", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		members := make([]*redis.Z, 0, 5)
		for index, postID := range []string{"601", "602", "603", "604", "605"} {
			members = append(members, &redis.Z{
				Score:  float64(now.Add(time.Duration(index-5) * time.Minute).Unix()),
				Member: postID,
			})
		}
		if _, err := client.ZAdd(key, members...).Result(); err != nil {
			t.Fatalf("seed bounded history: %v", err)
		}

		history, err := loadGuestRecommendationServedHistory(context.Background(), sessionID, now, cfg)
		if err != nil {
			t.Fatalf("load bounded served history: %v", err)
		}
		if len(history) != 3 {
			t.Fatalf("loaded history length=%d want 3: %#v", len(history), history)
		}
		for _, postID := range []uint{603, 604, 605} {
			if _, ok := history[postID]; !ok {
				t.Fatalf("newest post %d missing from bounded history: %#v", postID, history)
			}
		}
		for _, postID := range []uint{601, 602} {
			if _, ok := history[postID]; ok {
				t.Fatalf("oldest post %d was returned from bounded history", postID)
			}
		}
	})

	t.Run("ttl and hashed key", func(t *testing.T) {
		sessionID, key := newGuestRecommendationHistoryIntegrationSession(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		if err := recordGuestRecommendationServedPosts(context.Background(), sessionID, []uint{701}, now, cfg); err != nil {
			t.Fatalf("record served history: %v", err)
		}

		ttl, err := client.TTL(key).Result()
		if err != nil {
			t.Fatalf("read served history ttl: %v", err)
		}
		wantTTL := guestRecommendationHistoryTTL(cfg)
		if ttl <= 0 || ttl > wantTTL || ttl < wantTTL-5*time.Second {
			t.Fatalf("ttl=%s want >0 and within 5s of %s", ttl, wantTTL)
		}
		hashedExists, err := client.Exists(key).Result()
		if err != nil {
			t.Fatalf("check hashed history key: %v", err)
		}
		if hashedExists != 1 {
			t.Fatalf("hashed history key exists=%d want 1", hashedExists)
		}
		rawExists, err := client.Exists(sessionID).Result()
		if err != nil {
			t.Fatalf("check raw session key: %v", err)
		}
		if rawExists != 0 {
			t.Fatalf("raw session ID exists as Redis key=%d want 0", rawExists)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		sessionID, _ := newGuestRecommendationHistoryIntegrationSession(t, client)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := loadGuestRecommendationServedHistory(ctx, sessionID, time.Now().UTC(), cfg); err == nil {
			t.Fatal("canceled context unexpectedly loaded served history")
		}
	})
}

func openGuestRecommendationHistoryIntegrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	dbNumber := 0
	if rawDB := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); rawDB != "" {
		parsedDB, err := strconv.Atoi(rawDB)
		if err != nil {
			t.Fatalf("invalid REDIS_TEST_DB %q: %v", rawDB, err)
		}
		dbNumber = parsedDB
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_TEST_PASSWORD"),
		DB:       dbNumber,
	})
	if err := client.Ping().Err(); err != nil {
		_ = client.Close()
		t.Fatalf("ping Redis at %s: %v", addr, err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func newGuestRecommendationHistoryIntegrationSession(t *testing.T, client *redis.Client) (string, string) {
	t.Helper()
	sessionID := uuid.NewString()
	key := guestRecommendationHistoryKey(sessionID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete Redis test key %q: %v", key, err)
		}
	})
	return sessionID, key
}

func assertGuestRecommendationHistoryScore(t *testing.T, client *redis.Client, key, member string, want time.Time) {
	t.Helper()
	score, err := client.ZScore(key, member).Result()
	if err != nil {
		t.Fatalf("read score for %s: %v", member, err)
	}
	if got := int64(score); got != want.Unix() {
		t.Fatalf("score for %s=%d want %d", member, got, want.Unix())
	}
}
