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
			client.SRem(RegistryKey, id)
			client.ZRem(ExpiryCandidatesKey, id)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(id), 10))
		}
	})
	for _, id := range []uint{postID, liveID} {
		if _, err := store.Initialize(t.Context(), id, 1, 7, []uint{11}); err != nil {
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
	if _, err := store.LoadFullState(t.Context(), postID); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted full state=%v", err)
	}
	if _, err := store.Mutate(t.Context(), 12, postID, true); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("deleted mutation=%v", err)
	}
	states, missing, err := store.GetManyForServing(t.Context(), 11, []uint{postID, liveID}, time.Hour, time.Minute)
	if err != nil || len(states) != 1 || states[liveID].Count != 1 || len(missing) != 1 || missing[0] != postID {
		t.Fatalf("batch=%v unavailable=%v err=%v", states, missing, err)
	}
	if _, err := store.Initialize(t.Context(), postID, 1, 7, []uint{11}); !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("stale initializer=%v", err)
	}
	version := int64(7)
	if _, err := store.Recover(t.Context(), postID, FullState{Count: 1, Version: 7, UserIDs: []uint{11}}, RecoveryFence{ExpectedVersion: &version}); !errors.Is(err, ErrPostLikeUnavailable) {
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
