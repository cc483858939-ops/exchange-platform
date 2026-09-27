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

func TestStoreReadAwareExpiryLeaseIntegration(t *testing.T) {
	client, store, basePostID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	const fullTTL = 24 * time.Hour
	const renewalThreshold = 12 * time.Hour
	postIDs := make([]uint, 13)
	for index := range postIDs {
		postIDs[index] = basePostID + uint(index)
		if index > 0 {
			cleanupRecoverableStorePost(client, postIDs[index])
		}
	}
	t.Cleanup(func() {
		for _, postID := range postIDs {
			cleanupRecoverableStorePost(client, postID)
			cleanupRecoverableStoreBehaviorPair(client, 11, postID)
			cleanupRecoverableStoreBehaviorPair(client, 12, postID)
		}
	})

	initialize := func(postID uint, count int64, userIDs []uint) {
		t.Helper()
		if created, err := store.Initialize(ctx, postID, count, 10, userIDs); err != nil || !created {
			t.Fatalf("Initialize post=%d created=%t err=%v", postID, created, err)
		}
	}
	armWithPTTL := func(postID uint, pttl time.Duration) {
		t.Helper()
		if armed, err := store.ArmExpiry(ctx, postID, 10, fullTTL); err != nil || !armed {
			t.Fatalf("ArmExpiry post=%d armed=%t err=%v", postID, armed, err)
		}
		for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID), UsersKey(postID)} {
			if err := client.PExpire(key, pttl).Err(); err != nil {
				t.Fatalf("PEXPIRE key=%q: %v", key, err)
			}
		}
	}
	readPTTL := func(postID uint) time.Duration {
		t.Helper()
		pttl, err := client.PTTL(ReadyKey(postID)).Result()
		if err != nil {
			t.Fatalf("PTTL post=%d: %v", postID, err)
		}
		return pttl
	}

	// Persistent reads must stay pure and leave mutation chronology alone.
	persistentID := postIDs[0]
	initialize(persistentID, 1, []uint{11})
	candidateBefore, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(persistentID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.GetForServing(ctx, 11, persistentID, fullTTL, renewalThreshold)
	if err != nil || state.Count != 1 || !state.Liked {
		t.Fatalf("persistent serving state=%+v err=%v", state, err)
	}
	if pttl := readPTTL(persistentID); pttl != -1 {
		t.Fatalf("persistent Ready PTTL=%s want -1", pttl)
	}
	candidateAfter, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(persistentID), 10)).Result()
	if err != nil || candidateAfter != candidateBefore {
		t.Fatalf("candidate before=%f after=%f err=%v", candidateBefore, candidateAfter, err)
	}

	// Healthy leases are left alone, while low leases return to the full TTL.
	healthyID := postIDs[1]
	initialize(healthyID, 1, []uint{11})
	armWithPTTL(healthyID, 20*time.Hour)
	healthyBefore := readPTTL(healthyID)
	if _, err := store.GetForServing(ctx, 11, healthyID, fullTTL, renewalThreshold); err != nil {
		t.Fatal(err)
	}
	healthyAfter := readPTTL(healthyID)
	if healthyAfter <= 19*time.Hour || healthyAfter > healthyBefore {
		t.Fatalf("healthy Ready PTTL before=%s after=%s", healthyBefore, healthyAfter)
	}

	lowID := postIDs[2]
	initialize(lowID, 1, []uint{11})
	armWithPTTL(lowID, 5*time.Hour)
	if _, err := store.GetForServing(ctx, 11, lowID, fullTTL, renewalThreshold); err != nil {
		t.Fatal(err)
	}
	if pttl := readPTTL(lowID); pttl <= 23*time.Hour {
		t.Fatalf("renewed Ready PTTL=%s want near 24h", pttl)
	}
	for _, key := range []string{CountKey(lowID), VersionKey(lowID), UsersKey(lowID)} {
		pttl, err := client.PTTL(key).Result()
		if err != nil || pttl <= 23*time.Hour {
			t.Fatalf("renewed key=%q PTTL=%s err=%v", key, pttl, err)
		}
	}
	if marker, err := client.HGet(RecoverableVersionsKey, strconv.FormatUint(uint64(lowID), 10)).Result(); err != nil || marker != "10" {
		t.Fatalf("renewal marker=%q err=%v want unchanged version 10", marker, err)
	}
	if _, err := client.ZScore(ExpiryCandidatesKey, strconv.FormatUint(uint64(lowID), 10)).Result(); err != redis.Nil {
		t.Fatalf("renewal touched expiry candidates: %v", err)
	}

	// Internal reads do not renew; only a serving read crosses that boundary.
	internalID := postIDs[3]
	initialize(internalID, 1, []uint{11})
	armWithPTTL(internalID, 5*time.Hour)
	internalBefore := readPTTL(internalID)
	if _, err := store.Get(ctx, 11, internalID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSummary(ctx, internalID); err != nil {
		t.Fatal(err)
	}
	internalAfter := readPTTL(internalID)
	if internalAfter > internalBefore || internalAfter <= 4*time.Hour {
		t.Fatalf("internal read changed PTTL before=%s after=%s", internalBefore, internalAfter)
	}
	if _, err := store.GetForServing(ctx, 11, internalID, fullTTL, renewalThreshold); err != nil {
		t.Fatal(err)
	}
	if pttl := readPTTL(internalID); pttl <= 23*time.Hour {
		t.Fatalf("serving read after internal reads PTTL=%s want renewal", pttl)
	}

	// GetMany keeps one batch read and renews only low-TTL candidates, including
	// at most once when the input contains duplicate Post IDs.
	batchPersistentID, batchHealthyID, batchLowID := postIDs[4], postIDs[5], postIDs[6]
	initialize(batchPersistentID, 1, []uint{11})
	initialize(batchHealthyID, 1, []uint{11})
	initialize(batchLowID, 1, []uint{11})
	armWithPTTL(batchHealthyID, 20*time.Hour)
	armWithPTTL(batchLowID, 5*time.Hour)
	states, unavailable, err := store.GetManyForServing(ctx, 11, []uint{batchPersistentID, batchHealthyID, batchLowID, batchLowID}, fullTTL, renewalThreshold)
	if err != nil || len(unavailable) != 0 || len(states) != 3 {
		t.Fatalf("GetManyForServing states=%v unavailable=%v err=%v", states, unavailable, err)
	}
	if readPTTL(batchPersistentID) != -1 {
		t.Fatalf("batch persistent state gained a TTL: %s", readPTTL(batchPersistentID))
	}
	if pttl := readPTTL(batchHealthyID); pttl <= 19*time.Hour || pttl > 20*time.Hour {
		t.Fatalf("batch healthy state PTTL=%s", pttl)
	}
	if pttl := readPTTL(batchLowID); pttl <= 23*time.Hour {
		t.Fatalf("batch low state PTTL=%s want renewal", pttl)
	}

	// An absent Users Set is valid for an empty state and remains absent after renewal.
	emptyID := postIDs[7]
	initialize(emptyID, 0, nil)
	armWithPTTL(emptyID, 5*time.Hour)
	state, err = store.GetForServing(ctx, 11, emptyID, fullTTL, renewalThreshold)
	if err != nil || state.Count != 0 {
		t.Fatalf("empty state=%+v err=%v", state, err)
	}
	if pttl := readPTTL(emptyID); pttl <= 23*time.Hour {
		t.Fatalf("empty state Ready PTTL=%s want renewal", pttl)
	}
	if exists, err := client.Exists(UsersKey(emptyID)).Result(); err != nil || exists != 0 {
		t.Fatalf("empty Users key exists=%d err=%v", exists, err)
	}

	// A mutation after the observed version wins over a stale renewal attempt.
	raceID := postIDs[8]
	initialize(raceID, 1, []uint{11})
	armWithPTTL(raceID, 5*time.Hour)
	mutation, err := store.Mutate(ctx, 12, raceID, true)
	if err != nil || !mutation.Changed || mutation.Version != 11 {
		t.Fatalf("mutation=%+v err=%v", mutation, err)
	}
	if renewed, err := store.RenewExpiryLease(ctx, raceID, 10, fullTTL, renewalThreshold); err != nil || renewed {
		t.Fatalf("stale renewal renewed=%t err=%v", renewed, err)
	}
	for _, key := range []string{ReadyKey(raceID), CountKey(raceID), VersionKey(raceID), UsersKey(raceID)} {
		if pttl, err := client.PTTL(key).Result(); err != nil || pttl != -1 {
			t.Fatalf("mutation did not persist key=%q PTTL=%s err=%v", key, pttl, err)
		}
	}

	// Missing or mismatched recovery markers can never renew a lease.
	for index, markerValue := range []string{"missing", "9"} {
		markerID := postIDs[9+index]
		initialize(markerID, 1, []uint{11})
		armWithPTTL(markerID, 5*time.Hour)
		field := strconv.FormatUint(uint64(markerID), 10)
		if markerValue == "missing" {
			client.HDel(RecoverableVersionsKey, field)
		} else {
			client.HSet(RecoverableVersionsKey, field, markerValue)
		}
		if _, err := store.GetForServing(ctx, 11, markerID, fullTTL, renewalThreshold); err != nil {
			t.Fatal(err)
		}
		if pttl := readPTTL(markerID); pttl <= 0 || pttl > 5*time.Hour {
			t.Fatalf("marker=%q unexpectedly renewed PTTL=%s", markerValue, pttl)
		}
		if markerValue == "missing" {
			if exists, err := client.HExists(RecoverableVersionsKey, field).Result(); err != nil || exists {
				t.Fatalf("renewal recreated missing marker exists=%t err=%v", exists, err)
			}
		}
	}

	// A successful state read is returned even when its optional renewal fails.
	failOpenID := postIDs[11]
	initialize(failOpenID, 1, []uint{11})
	armWithPTTL(failOpenID, 5*time.Hour)
	renewAttempted := false
	state, err = store.get(ctx, 11, failOpenID, fullTTL, renewalThreshold, func(context.Context, uint, int64, time.Duration, time.Duration) (bool, error) {
		renewAttempted = true
		return false, errors.New("simulated optional renewal failure")
	})
	if err != nil || state.Count != 1 || !state.Liked || !renewAttempted {
		t.Fatalf("failed renewal state=%+v attempted=%t err=%v", state, renewAttempted, err)
	}
	if pttl := readPTTL(failOpenID); pttl <= 0 || pttl > 5*time.Hour {
		t.Fatalf("failed renewal unexpectedly changed PTTL=%s", pttl)
	}

	// A cold lease with no serving reads still expires naturally.
	coldID := postIDs[12]
	initialize(coldID, 0, nil)
	if armed, err := store.ArmExpiry(ctx, coldID, 10, fullTTL); err != nil || !armed {
		t.Fatalf("cold ArmExpiry armed=%t err=%v", armed, err)
	}
	for _, key := range []string{ReadyKey(coldID), CountKey(coldID), VersionKey(coldID)} {
		if err := client.PExpire(key, 150*time.Millisecond).Err(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if exists, err := client.Exists(ReadyKey(coldID)).Result(); err != nil || exists != 0 {
		t.Fatalf("cold Ready exists=%d err=%v", exists, err)
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
