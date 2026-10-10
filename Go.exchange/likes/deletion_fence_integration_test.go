package likes

import (
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestDeletedPostFenceSurvivesCleanupFailureAndStaleRecoveryIntegration(t *testing.T) {
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "false")
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { client.Close() })
	store := NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	liveID := postID + 1
	t.Cleanup(func() {
		for _, id := range []uint{postID, liveID} {
			client.Del(ReadyKey(id), CountKey(id), UsersKey(id), VersionKey(id))
			client.SRem(UserLikesKey(11), strconv.FormatUint(uint64(id), 10))
			client.ZRem(UserLikesOrderKey(11), strconv.FormatUint(uint64(id), 10))
			client.SRem(UserLikesKey(12), strconv.FormatUint(uint64(id), 10))
			client.ZRem(UserLikesOrderKey(12), strconv.FormatUint(uint64(id), 10))
			client.SRem(RegistryKey, id)
			client.ZRem(ExpiryCandidatesKey, id)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(id), 10))
		}
	})
	for _, id := range []uint{postID, liveID} {
		if _, err := initializeLikeStore(store, t.Context(), id, 1, 7, []uint{11}); err != nil {
			t.Fatal(err)
		}
	}
	// An unrelated type fault prevents cleanup, but must not undo the fence.
	if err := client.Del(CountKey(postID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(CountKey(postID), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("cleanup failure=%v", err)
	}
	if _, err := store.Get(t.Context(), 11, postID); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted read=%v", err)
	}
	if _, err := store.Get(t.Context(), postID+101, postID); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted Post with missing User state must remain unavailable, got %v", err)
	}
	if _, err := store.LoadFullState(t.Context(), postID); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted full state=%v", err)
	}
	if _, err := store.Mutate(t.Context(), 12, postID, true); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted mutation=%v", err)
	}
	states, missing, err := store.GetManyForServing(t.Context(), 11, []uint{postID, liveID}, 0, 0)
	if err != nil || len(states) != 1 || states[liveID].Count != 1 || len(missing) != 1 || missing[0] != postID {
		t.Fatalf("batch=%v unavailable=%v err=%v", states, missing, err)
	}
	if _, err := initializeLikeStore(store, t.Context(), postID, 1, 7, []uint{11}); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("stale initializer=%v", err)
	}
	version := int64(7)
	if _, err := recoverLikeStore(store, t.Context(), postID, FullState{Count: 1, Version: 7}, RecoveryFence{ExpectedVersion: &version}); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("stale recovery=%v", err)
	}
	if err := client.Del(CountKey(postID)).Err(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.DeletePost(t.Context(), postID); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := client.Get(ReadyKey(postID)).Result(); err != nil || ready != "deleted" {
		t.Fatalf("fence=%q err=%v", ready, err)
	}
	if ttl, err := client.TTL(ReadyKey(postID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("fence expires: ttl=%v err=%v", ttl, err)
	}
	if _, err := store.Get(t.Context(), 11, liveID); err != nil {
		t.Fatalf("unrelated state lost: %v", err)
	}
}
