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
	client.SRem(UserLikesKey(11), postIDString)
	client.SRem(UserLikesKey(12), postIDString)
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
	if err := store.InitializeUserEmpty(ctx, 11); err != nil {
		t.Fatal(err)
	}

	client.Set(ReadyKey(postID), "1", 0)
	client.Set(VersionKey(postID), "0", 0)
	if _, err := store.Get(ctx, 11, postID); !errors.Is(err, ErrPostLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("Get error=%v want Post NotReady compatible with ErrNotReady", err)
	}
	if _, err := store.LoadSnapshot(ctx, postID); !errors.Is(err, ErrPostLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("LoadSnapshot error=%v want Post NotReady compatible with ErrNotReady", err)
	}
	if _, err := store.LoadFullState(ctx, postID); !errors.Is(err, ErrPostLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("LoadFullState error=%v want Post NotReady compatible with ErrNotReady", err)
	}
	if _, err := store.Mutate(ctx, 11, postID, true); !errors.Is(err, ErrPostLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("Mutate error=%v want Post NotReady compatible with ErrNotReady", err)
	}

	client.Set(CountKey(postID), "0", 0)
	client.Del(VersionKey(postID))
	if _, err := store.Get(ctx, 11, postID); !errors.Is(err, ErrPostLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing Version error=%v want Post NotReady compatible with ErrNotReady", err)
	}
	client.Set(VersionKey(postID), "0", 0)
	client.Set(CountKey(postID), "2", 0)
	client.SAdd(UsersKey(postID), "11")
	state, err := store.Get(ctx, 11, postID)
	if err != nil || state.Count != 2 || state.Liked {
		t.Fatalf("legacy Post Users key affected new read state=%+v err=%v", state, err)
	}
	states, unavailable, err := store.GetMany(ctx, 11, []uint{postID})
	if err != nil || len(states) != 1 || len(unavailable) != 0 || states[postID].Count != 2 || states[postID].Liked {
		t.Fatalf("GetMany states=%v unavailable=%v", states, unavailable)
	}
}

func TestStoreInitializeCreatesManagedPersistentStateIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 2, 4, []uint{12, 11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	state, err := store.LoadFullState(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != 2 || state.Version != 4 {
		t.Fatalf("state=%+v", state)
	}
	for _, userID := range []uint{11, 12} {
		if initialized, err := client.SIsMember(UserLikesKey(userID), UserLikesInitSentinel).Result(); err != nil || !initialized {
			t.Fatalf("user=%d initialized=%t err=%v", userID, initialized, err)
		}
		if liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
			t.Fatalf("user=%d liked=%t err=%v", userID, liked, err)
		}
	}
	if exists, err := client.Exists(UsersKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("legacy Post Users key exists=%d err=%v", exists, err)
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
	for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID), UserLikesKey(11), UserLikesKey(12)} {
		if ttl, err := client.TTL(key).Result(); err != nil || ttl != -1 {
			t.Fatalf("key=%q ttl=%s err=%v want persistent", key, ttl, err)
		}
	}
}

func TestStoreManagedZeroLossCannotBootstrapIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
	created, err := recoverLikeStore(store, ctx, postID, FullState{}, RecoveryFence{AllowZeroBootstrap: true})
	if created || !errors.Is(err, ErrLikeRecoveryUnsafe) {
		t.Fatalf("managed zero recovery created=%t err=%v", created, err)
	}
	if exists, err := client.Exists(ReadyKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("Ready exists=%d err=%v", exists, err)
	}
}

func TestStoreNonzeroRecoveryIsUnsafeAndMutationRemainsAtomicIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	t.Cleanup(func() {
		cleanupRecoverableStoreBehaviorPair(client, 11, postID)
		cleanupRecoverableStoreBehaviorPair(client, 12, postID)
		client.SRem(UserLikesKey(11), strconv.FormatUint(uint64(postID), 10))
		client.SRem(UserLikesKey(12), strconv.FormatUint(uint64(postID), 10))
	})
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 1, 10, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	markerVersion := int64(10)
	if created, err := recoverLikeStore(store, ctx, postID, FullState{Count: 1, Version: 10}, RecoveryFence{ExpectedVersion: &markerVersion}); created || !errors.Is(err, ErrLikeRecoveryUnsafe) {
		t.Fatalf("nonzero Recover created=%t err=%v want unsafe", created, err)
	}
	if armed, err := store.ArmExpiry(ctx, postID, 10, time.Hour); armed || !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("ArmExpiry armed=%t err=%v want explicitly unsupported", armed, err)
	}
	if ttl, err := client.TTL(ReadyKey(postID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("unsupported expiry changed Ready TTL=%s err=%v", ttl, err)
	}
	if marker, err := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || marker {
		t.Fatalf("unsupported expiry wrote marker=%t err=%v", marker, err)
	}
	if _, err := store.Mutate(ctx, 12, postID, true); err != nil {
		t.Fatal(err)
	}
	if version, err := client.Get(VersionKey(postID)).Result(); err != nil || version != "11" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestStoreExpiryApisFailClosedIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if mutation, err := store.Mutate(ctx, 11, postID, true); err != nil || !mutation.Changed {
		t.Fatalf("initial mutation=%+v err=%v", mutation, err)
	}

	if armed, err := store.ArmExpiry(ctx, postID, 1, time.Hour); armed || !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	if renewed, err := store.RenewExpiryLease(ctx, postID, 1, time.Hour, time.Minute); renewed || !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("RenewExpiryLease renewed=%t err=%v", renewed, err)
	}
	if _, err := store.GetForServing(ctx, 11, postID, time.Hour, time.Minute); !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("GetForServing error=%v want explicit unsupported", err)
	}
	if _, _, err := store.GetManyForServing(ctx, 11, []uint{postID}, time.Hour, time.Minute); !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("GetManyForServing error=%v want explicit unsupported", err)
	}
	for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID), UserLikesKey(11)} {
		if ttl, err := client.TTL(key).Result(); err != nil || ttl != -1 {
			t.Fatalf("key=%q TTL=%s err=%v want persistent", key, ttl, err)
		}
	}
	if marker, err := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || marker {
		t.Fatalf("unsupported expiry wrote recovery marker=%t err=%v", marker, err)
	}
	state, err := store.Get(ctx, 11, postID)
	if err != nil || state.Count != 1 || state.Version != 1 || !state.Liked {
		t.Fatalf("expiry rejection changed state=%+v err=%v", state, err)
	}
}

func TestStoreIdempotentMutationPreservesUserRelationAndEventsIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, 11); err != nil {
		t.Fatal(err)
	}
	first, err := store.Mutate(ctx, 11, postID, true)
	if err != nil || !first.Changed || first.Count != 1 || first.Version != 1 {
		t.Fatalf("first mutation=%+v err=%v", first, err)
	}
	pair := BehaviorPair(11, postID)
	behaviorBefore, err := client.HGet(BehaviorStateKey, pair).Result()
	if err != nil {
		t.Fatal(err)
	}
	candidateBefore, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(postID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.Mutate(ctx, 11, postID, true)
	if err != nil || duplicate.Changed || duplicate.Count != 1 || duplicate.Version != 1 || !duplicate.Liked {
		t.Fatalf("duplicate mutation=%+v err=%v", duplicate, err)
	}
	behaviorAfter, err := client.HGet(BehaviorStateKey, pair).Result()
	if err != nil || behaviorAfter != behaviorBefore {
		t.Fatalf("duplicate changed Behavior state before=%q after=%q err=%v", behaviorBefore, behaviorAfter, err)
	}
	candidateAfter, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(postID), 10)).Result()
	if err != nil || candidateAfter != candidateBefore {
		t.Fatalf("duplicate changed candidate before=%v after=%v err=%v", candidateBefore, candidateAfter, err)
	}
	if initialized, err := client.SIsMember(UserLikesKey(11), UserLikesInitSentinel).Result(); err != nil || !initialized {
		t.Fatalf("User sentinel=%t err=%v", initialized, err)
	}
	if liked, err := client.SIsMember(UserLikesKey(11), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("User relation=%t err=%v", liked, err)
	}
	if exists, err := client.Exists(UsersKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("legacy Post Users key exists=%d err=%v", exists, err)
	}
}
func TestStoreLuaTypePreflightPreventsPurgePartialMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	if err := client.Set(RebuildTokenKey(postID), "stale-token", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(VersionKey(postID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.RPush(VersionKey(postID), "wrong Redis type").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgePost(ctx, postID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("Purge error=%v want ErrLikeRedisType", err)
	}
	for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("key=%q exists=%d err=%v after preflight failure", key, exists, err)
		}
	}
	if exists, err := client.Exists(RebuildTokenKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("rebuild token exists=%d err=%v; purge must revoke stale writers even when key preflight fails", exists, err)
	}
	if exists, err := client.Exists(UsersKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("legacy Post Users key exists=%d err=%v", exists, err)
	}
}

func TestStoreLuaTypePreflightPreventsRecoverPartialMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	client.Set(RecoverableVersionsKey, "wrong type", 0)
	t.Cleanup(func() { client.Del(RecoverableVersionsKey) })
	if created, err := recoverLikeStore(store, ctx, postID, FullState{}, RecoveryFence{AllowZeroBootstrap: true}); created || !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("Recover created=%t err=%v want ErrLikeRedisType", created, err)
	}
	if state, err := store.LoadFullState(ctx, postID); err != nil || state.Count != 1 || state.Version != 1 {
		t.Fatalf("state=%+v err=%v after preflight failure", state, err)
	}
}

func TestStoreExpiryEntryPointsRejectBeforeMutationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("Initialize created=%t err=%v", created, err)
	}
	armed, err := store.ArmExpiry(ctx, postID, 0, time.Hour)
	if armed || !errors.Is(err, ErrLikeStateExpiryUnsupported) {
		t.Fatalf("ArmExpiry armed=%t err=%v want explicit unsupported", armed, err)
	}
	if ttl, ttlErr := client.TTL(ReadyKey(postID)).Result(); ttlErr != nil || ttl != -1 {
		t.Fatalf("Ready ttl=%s err=%v after preflight failure", ttl, ttlErr)
	}
	if exists, markerErr := client.HExists(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result(); markerErr != nil || exists {
		t.Fatalf("marker exists=%t err=%v after unsupported expiry", exists, markerErr)
	}
}
