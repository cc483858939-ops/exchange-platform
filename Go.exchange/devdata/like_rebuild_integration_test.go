package devdata

import (
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

func TestDevDataMaintenanceReloadsCurrentLikesAndRejectsDeletedPostIntegration(t *testing.T) {
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
	schema := fmt.Sprintf("devdata_like_rebuild_%d", time.Now().UnixNano())
	for _, sql := range []string{
		"CREATE SCHEMA " + schema,
		"SET LOCAL search_path TO " + schema,
		"CREATE TABLE posts (id bigint PRIMARY KEY, like_count bigint, like_sync_version bigint, deleted_at timestamptz)",
		"CREATE TABLE post_reaction (post_id bigint, user_id bigint, reaction integer, liked boolean)",
	} {
		if err := tx.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	t.Cleanup(func() { client.Close() })
	store := likes.NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	liveID := postID + 1
	t.Cleanup(func() {
		for _, id := range []uint{postID, liveID} {
			client.Del(likes.ReadyKey(id), likes.CountKey(id), likes.UsersKey(id), likes.VersionKey(id), likes.RebuildTokenKey(id))
			client.SRem(likes.RegistryKey, id)
			client.ZRem(likes.ExpiryCandidatesKey, id)
			client.HDel(likes.RecoverableVersionsKey, strconv.FormatUint(uint64(id), 10))
		}
	})
	if err := tx.Exec("INSERT INTO posts VALUES (?, 1, 9, NULL)", postID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO post_reaction VALUES (?, 11, ?, true)", postID, models.PostReactionLike).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeLikeStore(store, t.Context(), liveID, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	maintenance := newSyncMaintenance()
	// A deleted Post remains fenced; request-time and maintenance code do not
	// reactivate its identity or restore a stale Like snapshot.
	maintenance.addNew(postID)
	maintenance.addPurge(liveID)
	if err := performPostCommitMaintenance(t.Context(), tx, client, maintenance); err == nil {
		t.Fatal("SQL-committed Like maintenance failure was not surfaced")
	}
	if _, err := store.LoadSummary(t.Context(), liveID); !errors.Is(err, likes.ErrPostLikeNotReady) {
		t.Fatalf("later purge was skipped after earlier initializer failure: %v", err)
	}
	if _, err := store.Get(t.Context(), 11, postID); !errors.Is(err, likes.ErrPostLikeUnavailable) {
		t.Fatalf("SPEC-01 unexpectedly reactivated nonzero state: %v", err)
	}
	if err := tx.Exec("UPDATE posts SET deleted_at=now() WHERE id=?", postID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if err := performPostCommitMaintenance(t.Context(), tx, client, maintenance); err == nil {
		t.Fatal("deleted Post initialization failure was not surfaced")
	}
	if _, err := store.Get(t.Context(), 11, postID); !errors.Is(err, likes.ErrPostLikeUnavailable) {
		t.Fatalf("deleted Post reactivated: %v", err)
	}
}
