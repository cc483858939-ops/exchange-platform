package tasks

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

func TestUserLikeRelationCleanupUsesSQLLifecycleAndPreservesSentinelIntegration(t *testing.T) {
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
	schema := fmt.Sprintf("user_like_cleanup_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz)",
		"CREATE TABLE posts (id bigint PRIMARY KEY, deleted_at timestamptz)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	t.Cleanup(func() { _ = client.Close() })
	store := likes.NewStore(client)
	baseID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID, deletedID, activeID, missingReadyActiveID, missingSQLID := baseID+101, baseID+1, baseID+2, baseID+3, baseID+4
	t.Cleanup(func() {
		_ = client.Del(likes.UserLikesKey(userID), likes.ReadyKey(deletedID), likes.CountKey(deletedID), likes.VersionKey(deletedID), likes.ReadyKey(activeID), likes.CountKey(activeID), likes.VersionKey(activeID), likes.ReadyKey(missingSQLID), likes.CountKey(missingSQLID), likes.VersionKey(missingSQLID)).Err()
	})
	if err := tx.Exec("INSERT INTO users (id) VALUES (?)", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO posts (id) VALUES (?), (?)", activeID, missingReadyActiveID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO posts (id, deleted_at) VALUES (?, now())", deletedID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.ReadyKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.CountKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.VersionKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), deletedID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), missingSQLID); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(likes.UserLikesKey(userID), deletedID, activeID, missingReadyActiveID, missingSQLID).Err(); err != nil {
		t.Fatal(err)
	}
	state := userLikeRelationCleanupState{}
	for i := 0; i < 4; i++ {
		if err := runUserLikeRelationCleanupPass(t.Context(), store, tx, &state); err != nil {
			t.Fatal(err)
		}
	}
	for member, want := range map[uint]bool{
		0: true, deletedID: false, activeID: true, missingReadyActiveID: true, missingSQLID: false,
	} {
		has, err := client.SIsMember(likes.UserLikesKey(userID), strconv.FormatUint(uint64(member), 10)).Result()
		if err != nil || has != want {
			t.Fatalf("User relation post=%d present=%t err=%v want=%t", member, has, err, want)
		}
	}
	if removed, err := store.RemoveDeletedUserPostRelations(t.Context(), userID, make([]uint, 129)); err == nil || removed != 0 {
		t.Fatalf("oversized cleanup batch removed=%d err=%v", removed, err)
	}
}
