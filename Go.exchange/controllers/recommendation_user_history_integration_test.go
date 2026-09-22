package controllers

import (
	"context"
	"encoding/binary"
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

func TestUserRecommendationServedHistoryRedisIntegration(t *testing.T) {
	client := openUserRecommendationHistoryIntegrationRedis(t)
	previousRedis := global.RedisDB
	global.RedisDB = client
	t.Cleanup(func() {
		global.RedisDB = previousRedis
	})

	cfg := config.RecommendationConfig{
		ServedHardExclusionMinutes: 30,
		ServedSoftLookbackDays:     7,
		ServedHistoryLimit:         3,
	}

	t.Run("write read round trip and deduplication", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{101, 102, 102, 0}, now, cfg); err != nil {
			t.Fatalf("record served history: %v", err)
		}
		cardinality, err := client.ZCard(key).Result()
		if err != nil {
			t.Fatalf("read round-trip history cardinality: %v", err)
		}
		if cardinality != 2 {
			t.Fatalf("round-trip history cardinality=%d want 2", cardinality)
		}
		for _, member := range []string{"101", "102"} {
			score, err := client.ZScore(key, member).Result()
			if err != nil {
				t.Fatalf("round-trip member %s was not stored: %v", member, err)
			}
			if int64(score) != now.Unix() {
				t.Fatalf("round-trip member %s score=%d want %d", member, int64(score), now.Unix())
			}
		}
		if _, err := client.ZScore(key, "0").Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("round-trip zero member score error=%v want redis.Nil", err)
		}

		history, err := loadUserRecommendationServedHistory(context.Background(), userID, now, cfg)
		if err != nil {
			t.Fatalf("load served history: %v", err)
		}
		if len(history) != 2 {
			t.Fatalf("round-trip loaded history length=%d want 2: %#v", len(history), history)
		}
		for _, postID := range []uint{101, 102} {
			served, ok := history[postID]
			if !ok || !served.Hard || served.Soft || !served.LastServedAt.Equal(now) {
				t.Fatalf("post %d classification=%#v present=%t want hard at %s", postID, served, ok, now)
			}
		}
	})

	t.Run("hard soft and exact soft boundary", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		softStart := now.AddDate(0, 0, -7)
		_, err := client.ZAdd(key,
			&redis.Z{Score: float64(now.Add(-5 * time.Minute).Unix()), Member: "201"},
			&redis.Z{Score: float64(now.Add(-2 * time.Hour).Unix()), Member: "202"},
			&redis.Z{Score: float64(softStart.Unix()), Member: "203"},
			&redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "204"},
		).Result()
		if err != nil {
			t.Fatalf("seed served history: %v", err)
		}

		history, err := loadUserRecommendationServedHistory(context.Background(), userID, now, cfg)
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
			t.Fatalf("exact soft-boundary post 203 remained active: %#v", history[203])
		}
		if _, ok := history[204]; ok {
			t.Fatalf("expired post 204 remained active: %#v", history[204])
		}
	})

	t.Run("timestamp refresh and soft becomes hard", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		softTime := now.Add(-2 * time.Hour)
		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{301}, softTime, cfg); err != nil {
			t.Fatalf("record soft served history: %v", err)
		}
		history, err := loadUserRecommendationServedHistory(context.Background(), userID, now, cfg)
		if err != nil {
			t.Fatalf("load soft served history: %v", err)
		}
		if served := history[301]; served.Hard || !served.Soft {
			t.Fatalf("initial classification=%#v want soft only", served)
		}

		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{301}, now, cfg); err != nil {
			t.Fatalf("refresh served history: %v", err)
		}
		assertUserRecommendationHistoryScore(t, client, key, "301", now)
		history, err = loadUserRecommendationServedHistory(context.Background(), userID, now, cfg)
		if err != nil {
			t.Fatalf("load refreshed served history: %v", err)
		}
		if served, ok := history[301]; !ok || !served.Hard || served.Soft || !served.LastServedAt.Equal(now) {
			t.Fatalf("refreshed classification=%#v present=%t want hard at %s", served, ok, now)
		}
	})

	t.Run("expired cleanup and cap", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		if _, err := client.ZAdd(key, &redis.Z{
			Score:  float64(now.Add(-8 * 24 * time.Hour).Unix()),
			Member: "401",
		}).Result(); err != nil {
			t.Fatalf("seed expired served history: %v", err)
		}
		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{402}, now, cfg); err != nil {
			t.Fatalf("record served history with expired entry: %v", err)
		}
		if _, err := client.ZScore(key, "401").Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("expired post 401 score error=%v want redis.Nil", err)
		}

		for index, postID := range []uint{501, 502, 503, 504} {
			if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{postID}, now.Add(time.Duration(index)*time.Second), cfg); err != nil {
				t.Fatalf("record post %d: %v", postID, err)
			}
		}
		cardinality, err := client.ZCard(key).Result()
		if err != nil {
			t.Fatalf("read capped history: %v", err)
		}
		if cardinality != 3 {
			t.Fatalf("history cardinality=%d want 3", cardinality)
		}
		if _, err := client.ZScore(key, "501").Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("oldest post 501 score error=%v want redis.Nil", err)
		}
	})

	t.Run("bounded read", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
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

		history, err := loadUserRecommendationServedHistory(context.Background(), userID, now, cfg)
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

	t.Run("ttl and ttl refresh", func(t *testing.T) {
		userID, key := newUserRecommendationHistoryIntegrationUser(t, client)
		now := time.Now().UTC().Truncate(time.Second)
		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{701}, now, cfg); err != nil {
			t.Fatalf("record served history: %v", err)
		}
		wantTTL := userRecommendationHistoryTTL(cfg)
		assertUserRecommendationHistoryTTL(t, client, key, wantTTL)

		if err := client.Expire(key, time.Hour).Err(); err != nil {
			t.Fatalf("shorten served history ttl: %v", err)
		}
		if err := recordUserRecommendationServedPosts(context.Background(), userID, []uint{702}, now.Add(time.Minute), cfg); err != nil {
			t.Fatalf("record refreshed served history: %v", err)
		}
		assertUserRecommendationHistoryTTL(t, client, key, wantTTL)
	})

	t.Run("canceled context", func(t *testing.T) {
		userID, _ := newUserRecommendationHistoryIntegrationUser(t, client)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := loadUserRecommendationServedHistory(ctx, userID, time.Now().UTC(), cfg); err == nil {
			t.Fatal("canceled context unexpectedly loaded served history")
		}
	})
}

func openUserRecommendationHistoryIntegrationRedis(t *testing.T) *redis.Client {
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
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newUserRecommendationHistoryIntegrationUser(t *testing.T, client *redis.Client) (uint, string) {
	t.Helper()
	identifier := uuid.New()
	userID64 := binary.BigEndian.Uint64(identifier[:8]) & uint64(^uint(0)>>1)
	if userID64 == 0 {
		userID64 = 1
	}
	userID := uint(userID64)
	key := userRecommendationHistoryKey(userID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete Redis test key %q: %v", key, err)
		}
	})
	return userID, key
}

func assertUserRecommendationHistoryScore(t *testing.T, client *redis.Client, key, member string, want time.Time) {
	t.Helper()
	score, err := client.ZScore(key, member).Result()
	if err != nil {
		t.Fatalf("read score for %s: %v", member, err)
	}
	if got := int64(score); got != want.Unix() {
		t.Fatalf("score for %s=%d want %d", member, got, want.Unix())
	}
}

func assertUserRecommendationHistoryTTL(t *testing.T, client *redis.Client, key string, want time.Duration) {
	t.Helper()
	ttl, err := client.TTL(key).Result()
	if err != nil {
		t.Fatalf("read served history ttl: %v", err)
	}
	if ttl <= 0 || ttl > want || ttl < want-5*time.Second {
		t.Fatalf("ttl=%s want >0 and within 5s of %s", ttl, want)
	}
}
