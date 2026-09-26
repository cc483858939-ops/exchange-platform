package recommendation

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestRedisHistoryStoreUserIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisHistoryStore(client)
	if err != nil {
		t.Fatal(err)
	}
	userID := uint(time.Now().UnixNano())
	key := userHistoryKey(userID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete user history test key: %v", err)
		}
	})

	now := time.Now().UTC().Truncate(time.Second)
	window := testHistoryWindow(now, 3, 8*24*time.Hour)
	if err := store.RecordUserServed(context.Background(), userID, []uint{101, 102, 102, 0}, window); err != nil {
		t.Fatalf("record user history: %v", err)
	}
	assertHistoryCardinality(t, client, key, 2)
	assertHistoryScore(t, client, key, "101", now)
	assertHistoryScore(t, client, key, "102", now)
	assertHistoryMissing(t, client, key, "0")
	assertHistoryTTL(t, client, key, window.TTL)

	history, err := store.LoadUserHistory(context.Background(), userID, window)
	if err != nil {
		t.Fatalf("load user history: %v", err)
	}
	for _, postID := range []uint{101, 102} {
		if item, ok := history[postID]; !ok || !item.Hard || item.Soft || !item.LastServedAt.Equal(now) {
			t.Fatalf("user post %d history=%#v present=%t", postID, item, ok)
		}
	}

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset user history: %v", err)
	}
	softStart := now.AddDate(0, 0, -7)
	if _, err := client.ZAdd(key,
		&redis.Z{Score: float64(now.Add(-5 * time.Minute).Unix()), Member: "201"},
		&redis.Z{Score: float64(now.Add(-2 * time.Hour).Unix()), Member: "202"},
		&redis.Z{Score: float64(softStart.Unix()), Member: "203"},
		&redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "204"},
	).Result(); err != nil {
		t.Fatalf("seed user history boundaries: %v", err)
	}
	history, err = store.LoadUserHistory(context.Background(), userID, window)
	if err != nil {
		t.Fatalf("load user history boundaries: %v", err)
	}
	if item := history[201]; !item.Hard || item.Soft {
		t.Fatalf("hard user history=%#v", item)
	}
	if item := history[202]; item.Hard || !item.Soft {
		t.Fatalf("soft user history=%#v", item)
	}
	if _, ok := history[203]; ok {
		t.Fatal("user history at exclusive soft boundary remained active")
	}
	if _, ok := history[204]; ok {
		t.Fatal("expired user history remained active")
	}
	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset user history before soft-trim check: %v", err)
	}
	if _, err := client.ZAdd(key, &redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "205"}).Result(); err != nil {
		t.Fatalf("seed expired user history: %v", err)
	}
	if err := store.RecordUserServed(context.Background(), userID, []uint{206}, window); err != nil {
		t.Fatalf("record user history after expiration: %v", err)
	}
	assertHistoryMissing(t, client, key, "205")

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset user history before bounded-read check: %v", err)
	}
	for index, postID := range []string{"601", "602", "603", "604", "605"} {
		if _, err := client.ZAdd(key, &redis.Z{Score: float64(now.Add(time.Duration(index-5) * time.Minute).Unix()), Member: postID}).Result(); err != nil {
			t.Fatalf("seed bounded user history: %v", err)
		}
	}
	history, err = store.LoadUserHistory(context.Background(), userID, window)
	if err != nil {
		t.Fatalf("load bounded user history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("bounded user history length=%d want 3: %#v", len(history), history)
	}
	for _, postID := range []uint{603, 604, 605} {
		if _, ok := history[postID]; !ok {
			t.Errorf("newest post %d missing from bounded user history", postID)
		}
	}

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset user history before cap check: %v", err)
	}
	for index, postID := range []uint{501, 502, 503, 504} {
		if err := store.RecordUserServed(context.Background(), userID, []uint{postID}, windowForTime(window, now.Add(time.Duration(index)*time.Second))); err != nil {
			t.Fatalf("record user post %d: %v", postID, err)
		}
	}
	assertHistoryCardinality(t, client, key, 3)
	assertHistoryMissing(t, client, key, "501")

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset user history before TTL refresh check: %v", err)
	}
	if err := store.RecordUserServed(context.Background(), userID, []uint{701}, window); err != nil {
		t.Fatalf("record user history for TTL check: %v", err)
	}
	if err := client.Expire(key, time.Hour).Err(); err != nil {
		t.Fatalf("shorten user history TTL: %v", err)
	}
	if err := store.RecordUserServed(context.Background(), userID, []uint{702}, windowForTime(window, now.Add(time.Minute))); err != nil {
		t.Fatalf("refresh user history TTL: %v", err)
	}
	assertHistoryTTL(t, client, key, window.TTL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.LoadUserHistory(ctx, userID, window); err == nil {
		t.Fatal("canceled context unexpectedly loaded user history")
	}
}

func TestRedisHistoryStoreGuestIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisHistoryStore(client)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	key := guestHistoryKey(sessionID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete guest history test key: %v", err)
		}
	})

	now := time.Now().UTC().Truncate(time.Second)
	window := testHistoryWindow(now, 3, 24*time.Hour)
	if err := store.RecordGuestServed(context.Background(), sessionID, []uint{101, 102, 102, 0}, window); err != nil {
		t.Fatalf("record guest history: %v", err)
	}
	assertHistoryCardinality(t, client, key, 2)
	assertHistoryScore(t, client, key, "101", now)
	assertHistoryScore(t, client, key, "102", now)
	assertHistoryMissing(t, client, key, "0")
	assertHistoryTTL(t, client, key, window.TTL)
	if strings.Contains(key, sessionID) {
		t.Fatalf("guest session ID leaked into Redis key %q", key)
	}
	if exists, err := client.Exists(sessionID).Result(); err != nil || exists != 0 {
		t.Fatalf("raw guest session key exists=%d err=%v want 0, nil", exists, err)
	}

	history, err := store.LoadGuestHistory(context.Background(), sessionID, window)
	if err != nil {
		t.Fatalf("load guest history: %v", err)
	}
	for _, postID := range []uint{101, 102} {
		if item, ok := history[postID]; !ok || !item.Hard || item.Soft || !item.LastServedAt.Equal(now) {
			t.Fatalf("guest post %d history=%#v present=%t", postID, item, ok)
		}
	}

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset guest history: %v", err)
	}
	if _, err := client.ZAdd(key,
		&redis.Z{Score: float64(now.Add(-5 * time.Minute).Unix()), Member: "201"},
		&redis.Z{Score: float64(now.Add(-2 * time.Hour).Unix()), Member: "202"},
		&redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "203"},
	).Result(); err != nil {
		t.Fatalf("seed guest history boundaries: %v", err)
	}
	history, err = store.LoadGuestHistory(context.Background(), sessionID, window)
	if err != nil {
		t.Fatalf("load guest history boundaries: %v", err)
	}
	if item := history[201]; !item.Hard || item.Soft {
		t.Fatalf("hard guest history=%#v", item)
	}
	if item := history[202]; item.Hard || !item.Soft {
		t.Fatalf("soft guest history=%#v", item)
	}
	if _, ok := history[203]; ok {
		t.Fatal("expired guest history remained active")
	}
	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset guest history before soft-trim check: %v", err)
	}
	if _, err := client.ZAdd(key, &redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).Unix()), Member: "204"}).Result(); err != nil {
		t.Fatalf("seed expired guest history: %v", err)
	}
	if err := store.RecordGuestServed(context.Background(), sessionID, []uint{205}, window); err != nil {
		t.Fatalf("record guest history after expiration: %v", err)
	}
	assertHistoryMissing(t, client, key, "204")

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset guest history before bounded-read check: %v", err)
	}
	for index, postID := range []string{"601", "602", "603", "604", "605"} {
		if _, err := client.ZAdd(key, &redis.Z{Score: float64(now.Add(time.Duration(index-5) * time.Minute).Unix()), Member: postID}).Result(); err != nil {
			t.Fatalf("seed bounded guest history: %v", err)
		}
	}
	history, err = store.LoadGuestHistory(context.Background(), sessionID, window)
	if err != nil {
		t.Fatalf("load bounded guest history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("bounded guest history length=%d want 3: %#v", len(history), history)
	}
	for _, postID := range []uint{603, 604, 605} {
		if _, ok := history[postID]; !ok {
			t.Errorf("newest post %d missing from bounded guest history", postID)
		}
	}

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset guest history before cap check: %v", err)
	}
	for index, postID := range []uint{501, 502, 503, 504} {
		if err := store.RecordGuestServed(context.Background(), sessionID, []uint{postID}, windowForTime(window, now.Add(time.Duration(index)*time.Second))); err != nil {
			t.Fatalf("record guest post %d: %v", postID, err)
		}
	}
	assertHistoryCardinality(t, client, key, 3)
	assertHistoryMissing(t, client, key, "501")

	if err := client.Del(key).Err(); err != nil {
		t.Fatalf("reset guest history before TTL refresh check: %v", err)
	}
	if err := store.RecordGuestServed(context.Background(), sessionID, []uint{701}, window); err != nil {
		t.Fatalf("record guest history for TTL check: %v", err)
	}
	if err := client.Expire(key, time.Hour).Err(); err != nil {
		t.Fatalf("shorten guest history TTL: %v", err)
	}
	if err := store.RecordGuestServed(context.Background(), sessionID, []uint{702}, windowForTime(window, now.Add(time.Minute))); err != nil {
		t.Fatalf("refresh guest history TTL: %v", err)
	}
	assertHistoryTTL(t, client, key, window.TTL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.LoadGuestHistory(ctx, sessionID, window); err == nil {
		t.Fatal("canceled context unexpectedly loaded guest history")
	}
}

func openHistoryIntegrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	dbNumber := 0
	if rawDB := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); rawDB != "" {
		parsed, err := strconv.Atoi(rawDB)
		if err != nil {
			t.Fatalf("invalid REDIS_TEST_DB %q: %v", rawDB, err)
		}
		dbNumber = parsed
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_TEST_PASSWORD"), DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		_ = client.Close()
		t.Fatalf("ping Redis at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func testHistoryWindow(now time.Time, limit int, ttl time.Duration) HistoryWindow {
	return HistoryWindow{
		Now:       now,
		HardStart: now.Add(-30 * time.Minute),
		SoftStart: now.AddDate(0, 0, -7),
		Limit:     limit,
		TTL:       ttl,
	}
}

func windowForTime(window HistoryWindow, now time.Time) HistoryWindow {
	window.Now = now
	window.HardStart = now.Add(-30 * time.Minute)
	window.SoftStart = now.AddDate(0, 0, -7)
	return window
}

func assertHistoryCardinality(t *testing.T, client *redis.Client, key string, want int64) {
	t.Helper()
	got, err := client.ZCard(key).Result()
	if err != nil {
		t.Fatalf("read history cardinality: %v", err)
	}
	if got != want {
		t.Fatalf("history cardinality=%d want %d", got, want)
	}
}

func assertHistoryScore(t *testing.T, client *redis.Client, key, member string, want time.Time) {
	t.Helper()
	got, err := client.ZScore(key, member).Result()
	if err != nil {
		t.Fatalf("read history score for %s: %v", member, err)
	}
	if int64(got) != want.Unix() {
		t.Fatalf("history score for %s=%d want %d", member, int64(got), want.Unix())
	}
}

func assertHistoryMissing(t *testing.T, client *redis.Client, key, member string) {
	t.Helper()
	if _, err := client.ZScore(key, member).Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("history member %s error=%v want redis.Nil", member, err)
	}
}

func assertHistoryTTL(t *testing.T, client *redis.Client, key string, want time.Duration) {
	t.Helper()
	got, err := client.TTL(key).Result()
	if err != nil {
		t.Fatalf("read history TTL: %v", err)
	}
	if got <= 0 || got > want || got < want-5*time.Second {
		t.Fatalf("history TTL=%s want >0 and within 5s of %s", got, want)
	}
}
