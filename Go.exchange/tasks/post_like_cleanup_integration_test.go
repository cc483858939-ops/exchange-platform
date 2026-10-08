package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
)

func TestPostLikeCleanupRetainsWorkAcrossOutageAndRetriesIntegration(t *testing.T) {
	dsn, addr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("like_cleanup_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.PostLikeCleanup{}); err != nil {
		t.Fatal(err)
	}
	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: redisDB})
	t.Cleanup(func() { client.Close() })
	store := likes.NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	t.Cleanup(func() {
		client.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		client.SRem(likes.RegistryKey, postID)
		client.ZRem(likes.ExpiryCandidatesKey, postID)
	})
	if _, err := store.Initialize(t.Context(), postID, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := tx.Create(&models.PostLikeCleanup{PostID: postID, RetryAfter: now}).Error; err != nil {
		t.Fatal(err)
	}
	outage := errors.New("Redis unavailable")
	if err := runPostLikeCleanupPass(t.Context(), tx, func(context.Context, uint) error { return outage }, now); !errors.Is(err, outage) {
		t.Fatalf("outage=%v", err)
	}
	var queued models.PostLikeCleanup
	if err := tx.First(&queued).Error; err != nil || queued.PostID != postID || !queued.RetryAfter.After(now) {
		t.Fatalf("durable retry=%+v err=%v", queued, err)
	}
	called := false
	if err := runPostLikeCleanupPass(t.Context(), tx, func(context.Context, uint) error { called = true; return nil }, now); err != nil || called {
		t.Fatalf("backoff bypassed: called=%t err=%v", called, err)
	}
	if err := runPostLikeCleanupPass(t.Context(), tx, store.DeletePost, queued.RetryAfter); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Model(&models.PostLikeCleanup{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("acknowledged queue count=%d err=%v", count, err)
	}
	if _, err := store.Get(t.Context(), 1, postID); !errors.Is(err, likes.ErrPostLikeUnavailable) {
		t.Fatalf("recovered Redis served deleted state: %v", err)
	}
	if err := runPostLikeCleanupPass(t.Context(), tx, store.DeletePost, now.Add(time.Minute)); err != nil {
		t.Fatalf("empty replay pass: %v", err)
	}
}
