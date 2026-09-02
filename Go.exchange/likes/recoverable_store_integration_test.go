package likes

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func openRecoverableStoreIntegration(t *testing.T) (*redis.Client, *Store, uint) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	cleanup := func() { cleanupRecoverableStorePost(client, postID) }
	cleanup()
	t.Cleanup(func() {
		cleanup()
		client.Close()
	})
	return client, NewStore(client), postID
}

func cleanupRecoverableStorePost(client *redis.Client, postID uint) {
	postIDString := strconv.FormatUint(uint64(postID), 10)
	client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
	client.SRem(RegistryKey, postIDString)
	client.ZRem(ExpiryCandidatesKey, postIDString)
	client.HDel(RecoverableVersionsKey, postIDString)
	client.SRem(DirtyKey, postIDString)
	client.ZRem(ProcessingKey, postIDString)
	client.HDel(ClaimsKey, postIDString)
}

func cleanupRecoverableStoreBehaviorPair(client *redis.Client, userID, postID uint) {
	pair := BehaviorPair(userID, postID)
	client.SRem(BehaviorDirtyKey, pair)
	client.HDel(BehaviorStateKey, pair)
	client.ZRem(BehaviorProcessingKey, pair)
	client.HDel(BehaviorClaimsKey, pair)
}

func TestStoreIncompleteStateIsNotReadyIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()

	client.Set(ReadyKey(postID), "1", 0)
	client.Set(VersionKey(postID), "0", 0)
	if _, err := store.Get(ctx, 11, postID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Get error=%v want ErrNotReady", err)
	}
	if _, err := store.LoadSnapshot(ctx, postID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("LoadSnapshot error=%v want ErrNotReady", err)
	}
	if _, err := store.LoadFullState(ctx, postID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("LoadFullState error=%v want ErrNotReady", err)
	}
	if _, err := store.Mutate(ctx, 11, postID, true); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Mutate error=%v want ErrNotReady", err)
	}

	client.Set(CountKey(postID), "0", 0)
	client.Del(VersionKey(postID))
	if _, err := store.Get(ctx, 11, postID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing Version error=%v want ErrNotReady", err)
	}
	client.Set(VersionKey(postID), "0", 0)
	client.Set(CountKey(postID), "2", 0)
	client.SAdd(UsersKey(postID), "11")
	if _, err := store.Get(ctx, 11, postID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("cardinality mismatch error=%v want ErrNotReady", err)
	}
	states, unavailable, err := store.GetMany(ctx, 11, []uint{postID})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 || len(unavailable) != 1 || unavailable[0] != postID {
		t.Fatalf("GetMany states=%v unavailable=%v", states, unavailable)
	}
}

func TestStoreInitializeCreatesManagedPersistentStateIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 2, 4, []uint{12, 11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	state, err := store.LoadFullState(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != 2 || state.Version != 4 || !equalRecoverableUintSlices(state.UserIDs, []uint{11, 12}) {
		t.Fatalf("state=%+v", state)
	}
	if member, err := client.SIsMember(RegistryKey, postID).Result(); err != nil || !member {
		t.Fatalf("registry member=%t err=%v", member, err)
	}
	if _, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil {
		t.Fatalf("expiry candidate error=%v", err)
	}
	if exists, err := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || exists {
		t.Fatalf("recoverable marker exists=%t err=%v", exists, err)
	}
	for _, key := range []string{ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID)} {
		if ttl, err := client.TTL(key).Result(); err != nil || ttl != -1 {
			t.Fatalf("key=%q ttl=%s err=%v want persistent", key, ttl, err)
		}
	}
}

func TestStoreManagedZeroLossCannotBootstrapIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
	created, err := store.Recover(ctx, postID, FullState{}, RecoveryFence{AllowZeroBootstrap: true})
	if created || !errors.Is(err, ErrLikeRecoveryUnsafe) {
		t.Fatalf("managed zero recovery created=%t err=%v", created, err)
	}
	if exists, err := client.Exists(ReadyKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("Ready exists=%d err=%v", exists, err)
	}
}

func TestStoreMarkerRecoveryAndMutationFenceIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	t.Cleanup(func() {
		cleanupRecoverableStoreBehaviorPair(client, 11, postID)
		cleanupRecoverableStoreBehaviorPair(client, 12, postID)
	})
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 1, 10, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	armed, err := store.ArmExpiry(ctx, postID, 10, time.Hour)
	if err != nil || !armed {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	if marker, err := client.HGet(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || marker != "10" {
		t.Fatalf("marker=%q err=%v", marker, err)
	}
	client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
	markerVersion := int64(10)
	if created, err := store.Recover(ctx, postID, FullState{Count: 1, Version: 10, UserIDs: []uint{11}}, RecoveryFence{ExpectedVersion: &markerVersion}); err != nil || !created {
		t.Fatalf("marker Recover created=%t err=%v", created, err)
	}
	if _, err := store.Mutate(ctx, 12, postID, true); err != nil {
		t.Fatal(err)
	}
	if version, err := client.Get(VersionKey(postID)).Result(); err != nil || version != "11" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	if ttl, err := client.TTL(ReadyKey(postID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("mutated Ready ttl=%s err=%v", ttl, err)
	}
	if exists, err := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || exists {
		t.Fatalf("marker after mutation exists=%t err=%v", exists, err)
	}
}

func TestStoreRecoveryFenceAndExpiryRacesIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	armFirstPostID := postID + 1
	mutateFirstPostID := postID + 2
	t.Cleanup(func() {
		cleanupRecoverableStoreBehaviorPair(client, 11, armFirstPostID)
		cleanupRecoverableStoreBehaviorPair(client, 11, mutateFirstPostID)
	})
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 1, 10, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	if armed, err := store.ArmExpiry(ctx, postID, 10, time.Hour); err != nil || !armed {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
	wrongVersion := int64(9)
	if created, err := store.Recover(ctx, postID, FullState{Count: 1, Version: 9, UserIDs: []uint{11}}, RecoveryFence{ExpectedVersion: &wrongVersion}); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("mismatched recovery created=%t err=%v", created, err)
	}
	if exists, err := client.Exists(ReadyKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("mismatched recovery recreated state exists=%d err=%v", exists, err)
	}
	if err := store.PurgePost(ctx, postID); err != nil {
		t.Fatal(err)
	}
	if created, err := store.Recover(ctx, postID, FullState{Count: 1, Version: 10, UserIDs: []uint{11}}, RecoveryFence{ExpectedVersion: &wrongVersion}); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("purged recovery created=%t err=%v", created, err)
	}

	cleanupRecoverableStorePost(client, armFirstPostID)
	t.Cleanup(func() { cleanupRecoverableStorePost(client, armFirstPostID) })
	if created, err := store.Initialize(ctx, armFirstPostID, 0, 0, nil); err != nil || !created {
		t.Fatalf("race Initialize created=%t err=%v", created, err)
	}
	if armed, err := store.ArmExpiry(ctx, armFirstPostID, 0, time.Hour); err != nil || !armed {
		t.Fatalf("zero ArmExpiry armed=%t err=%v", armed, err)
	}
	if mutation, err := store.Mutate(ctx, 11, armFirstPostID, true); err != nil || !mutation.Changed || mutation.Version != 1 {
		t.Fatalf("arm-first mutation=%+v err=%v", mutation, err)
	}
	if ttl, err := client.TTL(ReadyKey(armFirstPostID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("arm-first Ready ttl=%s err=%v", ttl, err)
	}

	cleanupRecoverableStorePost(client, mutateFirstPostID)
	t.Cleanup(func() { cleanupRecoverableStorePost(client, mutateFirstPostID) })
	if created, err := store.Initialize(ctx, mutateFirstPostID, 0, 0, nil); err != nil || !created {
		t.Fatalf("second Initialize created=%t err=%v", created, err)
	}
	if mutation, err := store.Mutate(ctx, 11, mutateFirstPostID, true); err != nil || !mutation.Changed || mutation.Version != 1 {
		t.Fatalf("mutate-first mutation=%+v err=%v", mutation, err)
	}
	if armed, err := store.ArmExpiry(ctx, mutateFirstPostID, 0, time.Hour); err != nil || armed {
		t.Fatalf("stale ArmExpiry armed=%t err=%v", armed, err)
	}
	if ttl, err := client.TTL(ReadyKey(mutateFirstPostID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("mutate-first Ready ttl=%s err=%v", ttl, err)
	}
}

func TestStoreIdempotentMutationPreservesArmedExpiryIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	if armed, err := store.ArmExpiry(ctx, postID, 1, time.Hour); err != nil || !armed {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	mutation, err := store.Mutate(ctx, 11, postID, true)
	if err != nil || mutation.Changed || mutation.Version != 1 {
		t.Fatalf("idempotent mutation=%+v err=%v", mutation, err)
	}
	if ttl, err := client.TTL(ReadyKey(postID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("idempotent Ready ttl=%s err=%v", ttl, err)
	}
	if exists, err := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !exists {
		t.Fatalf("idempotent marker exists=%t err=%v", exists, err)
	}
	if _, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != redis.Nil {
		t.Fatalf("idempotent candidate err=%v", err)
	}
}

func TestStoreLuaTypePreflightPreventsPurgePartialMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Set(UsersKey(postID), "wrong type", 0)
	if err := store.PurgePost(ctx, postID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("Purge error=%v want ErrLikeRedisType", err)
	}
	for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID), UsersKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("key=%q exists=%d err=%v after preflight failure", key, exists, err)
		}
	}
}

func TestStoreLuaTypePreflightPreventsRecoverPartialMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Set(RecoverableVersionsKey, "wrong type", 0)
	t.Cleanup(func() { client.Del(RecoverableVersionsKey) })
	version := int64(1)
	if created, err := store.Recover(ctx, postID, FullState{Count: 1, Version: 1, UserIDs: []uint{11}}, RecoveryFence{ExpectedVersion: &version}); created || !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("Recover created=%t err=%v want ErrLikeRedisType", created, err)
	}
	if state, err := store.LoadFullState(ctx, postID); err != nil || state.Count != 1 || state.Version != 1 || !equalRecoverableUintSlices(state.UserIDs, []uint{11}) {
		t.Fatalf("state=%+v err=%v after preflight failure", state, err)
	}
}

func TestStoreLuaTypePreflightPreventsArmExpiryPartialMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := store.Initialize(ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Del(ExpiryCandidatesKey)
	client.Set(ExpiryCandidatesKey, "wrong type", 0)
	t.Cleanup(func() { client.Del(ExpiryCandidatesKey) })
	armed, err := store.ArmExpiry(ctx, postID, 0, time.Hour)
	if armed || !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("ArmExpiry armed=%t err=%v want ErrLikeRedisType", armed, err)
	}
	if ttl, ttlErr := client.TTL(ReadyKey(postID)).Result(); ttlErr != nil || ttl != -1 {
		t.Fatalf("Ready ttl=%s err=%v after preflight failure", ttl, ttlErr)
	}
	if exists, markerErr := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); markerErr != nil || exists {
		t.Fatalf("marker exists=%t err=%v after preflight failure", exists, markerErr)
	}
}

func equalRecoverableUintSlices(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
