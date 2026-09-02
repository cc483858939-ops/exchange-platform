package devdata

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

func TestDevDataReactivationRestoresRedisLikeStateIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test (DevData reactivation)")
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	defer client.Close()

	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	cleanup := func() {
		postIDString := strconv.FormatUint(uint64(postID), 10)
		client.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		client.SRem(likes.RegistryKey, postIDString)
		client.ZRem(likes.ExpiryCandidatesKey, postIDString)
		client.HDel(likes.RecoverableVersionsKey, postIDString)
		client.SRem(likes.DirtyKey, postIDString)
		client.ZRem(likes.ProcessingKey, postIDString)
		client.HDel(likes.ClaimsKey, postIDString)
	}
	cleanup()
	defer cleanup()

	ctx := context.Background()
	store := likes.NewStore(client)
	if created, err := store.Initialize(ctx, postID, 2, 17, []uint{11, 13}); err != nil || !created {
		t.Fatalf("seed like state created=%t err=%v", created, err)
	}
	if err := store.PurgePost(ctx, postID); err != nil {
		t.Fatalf("purge tombstoned Post like state: %v", err)
	}
	if _, err := store.Get(ctx, 11, postID); err != likes.ErrNotReady {
		t.Fatalf("purged state error=%v, want ErrNotReady", err)
	}

	maintenance := newSyncMaintenance()
	maintenance.addReactivation(postID, likes.FullState{Count: 2, Version: 17, UserIDs: []uint{11, 13}})
	performPostCommitMaintenance(ctx, client, maintenance)

	for _, userID := range []uint{11, 13} {
		state, err := store.Get(ctx, userID, postID)
		if err != nil {
			t.Fatalf("load reactivated like state for user %d: %v", userID, err)
		}
		if state.Count != 2 || state.Version != 17 || !state.Liked {
			t.Fatalf("reactivated state for user %d=%#v", userID, state)
		}
	}
}
