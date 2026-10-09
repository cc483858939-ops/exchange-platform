package devdata

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

func TestDevDataReactivationFailsClosedUntilUserLifecycleIntegration(t *testing.T) {
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
		client.SRem(likes.UserLikesKey(11), postIDString)
		client.SRem(likes.UserLikesKey(13), postIDString)
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
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize zero state created=%t err=%v", created, err)
	}
	for _, userID := range []uint{11, 13} {
		if err := store.InitializeUserEmpty(ctx, userID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Mutate(ctx, userID, postID, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PurgePost(ctx, postID); err != nil {
		t.Fatalf("purge Post aggregate state: %v", err)
	}
	if _, err := store.Get(ctx, 11, postID); err != likes.ErrNotReady {
		t.Fatalf("purged state error=%v, want ErrNotReady", err)
	}

	loaded := false
	if created, err := store.InitializeFrom(ctx, postID, true, func(context.Context) (likes.FullState, error) {
		loaded = true
		return likes.FullState{}, nil
	}); created || !errors.Is(err, likes.ErrLikeRecoveryUnsafe) {
		t.Fatalf("reactivation created=%t err=%v want explicit unsafe", created, err)
	}
	if loaded {
		t.Fatal("reactivation loaded a baseline without lifecycle support")
	}
	if exists, err := client.Exists(likes.RebuildTokenKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("unsafe reactivation acquired a token: exists=%d err=%v", exists, err)
	}
	if liked, err := client.SIsMember(likes.UserLikesKey(11), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("PurgePost changed relation membership=%t err=%v", liked, err)
	}
}
