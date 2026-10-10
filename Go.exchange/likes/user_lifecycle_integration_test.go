package likes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/go-redis/redis/v7"
)

func TestUserLikeTTLInitializationAndMutationsIntegration(t *testing.T) {
	settings := userLikeIntegrationSettings(true, 10*time.Second)
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, settings)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	if created, err := store.InitializeUserEmptyWithResult(ctx, userID); err != nil || !created {
		t.Fatalf("initialize User Set created=%t err=%v", created, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatal(err)
	}
	assertPersistentPostLikeKeys(t, client, postID)

	first, err := store.Mutate(ctx, userID, postID, true)
	if err != nil || !first.Changed || first.Count != 1 || first.Version != 1 {
		t.Fatalf("first Like=%+v err=%v", first, err)
	}
	expiry1 := readUserLikeLedgerExpiry(t, client, userID)
	behaviorPair := BehaviorPair(userID, postID)
	behaviorState, err := client.HGet(BehaviorStateKey, behaviorPair).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SRem(BehaviorDirtyKey, behaviorPair).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	duplicate, err := store.Mutate(ctx, userID, postID, true)
	if err != nil || duplicate.Changed || duplicate.Count != 1 || duplicate.Version != 1 {
		t.Fatalf("idempotent Like=%+v err=%v", duplicate, err)
	}
	expiry2 := readUserLikeLedgerExpiry(t, client, userID)
	if expiry2 <= expiry1 {
		t.Fatalf("idempotent Like did not refresh Ledger expiry: before=%d after=%d", expiry1, expiry2)
	}
	if dirty, err := client.SIsMember(BehaviorDirtyKey, behaviorPair).Result(); err != nil || dirty {
		t.Fatalf("idempotent Like generated Behavior work dirty=%t err=%v", dirty, err)
	}
	if current, err := client.HGet(BehaviorStateKey, behaviorPair).Result(); err != nil || current != behaviorState {
		t.Fatalf("idempotent Like changed Behavior state from %q to %q err=%v", behaviorState, current, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatal(err)
	}

	unlike, err := store.Mutate(ctx, userID, postID, false)
	if err != nil || !unlike.Changed || unlike.Liked || unlike.Count != 0 || unlike.Version != 2 {
		t.Fatalf("Unlike=%+v err=%v", unlike, err)
	}
	expiry3 := readUserLikeLedgerExpiry(t, client, userID)
	if expiry3 <= expiry2 {
		t.Fatalf("Unlike did not refresh Ledger expiry: before=%d after=%d", expiry2, expiry3)
	}
	if member, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || member {
		t.Fatalf("Unlike relation remains member=%t err=%v", member, err)
	}
	if sentinel, err := client.SIsMember(UserLikesKey(userID), UserLikesInitSentinel).Result(); err != nil || !sentinel {
		t.Fatalf("Unlike removed initialization sentinel=%t err=%v", sentinel, err)
	}
	if err := client.SRem(BehaviorDirtyKey, behaviorPair).Err(); err != nil {
		t.Fatal(err)
	}
	behaviorState, err = client.HGet(BehaviorStateKey, behaviorPair).Result()
	if err != nil {
		t.Fatal(err)
	}
	duplicateUnlike, err := store.Mutate(ctx, userID, postID, false)
	if err != nil || duplicateUnlike.Changed || duplicateUnlike.Count != 0 || duplicateUnlike.Version != 2 {
		t.Fatalf("idempotent Unlike=%+v err=%v", duplicateUnlike, err)
	}
	if readUserLikeLedgerExpiry(t, client, userID) <= expiry3 {
		t.Fatal("idempotent Unlike did not refresh Ledger expiry")
	}
	if dirty, err := client.SIsMember(BehaviorDirtyKey, behaviorPair).Result(); err != nil || dirty {
		t.Fatalf("idempotent Unlike generated Behavior work dirty=%t err=%v", dirty, err)
	}
	if current, err := client.HGet(BehaviorStateKey, behaviorPair).Result(); err != nil || current != behaviorState {
		t.Fatalf("idempotent Unlike changed Behavior state from %q to %q err=%v", behaviorState, current, err)
	}
	assertPersistentPostLikeKeys(t, client, postID)
}

func TestUserLikeArmingDisabledPersistsAndRemovesLedgerIntegration(t *testing.T) {
	settings := userLikeIntegrationSettings(false, time.Hour)
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, settings)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := client.PExpire(UserLikesKey(userID), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10), time.Now().Add(time.Minute).UnixMilli()).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, userID, postID, false); err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.PTTL(UserLikesKey(userID)).Result(); err != nil || ttl >= 0 {
		t.Fatalf("arming-disabled mutation left User Set TTL=%s err=%v", ttl, err)
	}
	if exists, err := client.Exists(UserLikesKey(userID)).Result(); err != nil || exists != 1 {
		t.Fatalf("arming-disabled mutation lost User Set exists=%d err=%v", exists, err)
	}
	if exists, err := client.HExists(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10)).Result(); err != nil || exists {
		t.Fatalf("arming-disabled mutation left Ledger field=%t err=%v", exists, err)
	}
}

func TestUserLikeColdClassificationFailsClosedIntegration(t *testing.T) {
	settings := userLikeIntegrationSettings(true, 250*time.Millisecond)
	client, store, userID, _ := openUserLikeLifecycleRedisIntegration(t, settings)
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		exists, err := client.Exists(UserLikesKey(userID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("User Like Set did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, err := store.InspectUserLikeState(t.Context(), userID)
	if err != nil || state.Status != "cold" || state.ExpiresAt.IsZero() {
		t.Fatalf("expired User Like state=%+v err=%v", state, err)
	}
	if ttl, err := client.TTL(UserLikesExpiryLedgerKey).Result(); err != nil || ttl != -1 {
		t.Fatalf("Ledger TTL=%s err=%v want persistent", ttl, err)
	}
	if cold, err := store.IsUserLikeCold(t.Context(), userID); err != nil || !cold {
		t.Fatalf("cold classification=%t err=%v", cold, err)
	}

	missingID := userID + 1
	if state, err := store.InspectUserLikeState(t.Context(), missingID); err != nil || state.Status != "unexpected_missing" {
		t.Fatalf("unregistered missing state=%+v err=%v", state, err)
	}
	futureID := userID + 2
	if err := client.HSet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(futureID), 10), time.Now().Add(time.Minute).UnixMilli()).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectUserLikeState(t.Context(), futureID); !errors.Is(err, ErrUserLikeRecoveryUnsafe) {
		t.Fatalf("prematurely missing state error=%v want unsafe", err)
	}

	ledgerKey := fmt.Sprintf("it:user-like-ledger:%d", userID)
	lockKey := fmt.Sprintf("it:user-like-lock:%d", userID)
	tempSetKey := fmt.Sprintf("it:user-like-set:%d", userID)
	noSentinelKey := fmt.Sprintf("it:user-like-no-sentinel:%d", userID)
	t.Cleanup(func() { _ = client.Del(ledgerKey, lockKey, tempSetKey, noSentinelKey).Err() })
	missingResult, err := inspectUserLikeStateScript.Run(client, []string{tempSetKey, ledgerKey, lockKey}, missingID).Result()
	if err != nil || asString(missingResult.([]interface{})[0]) != "unexpected_missing" {
		t.Fatalf("script missing result=%v err=%v", missingResult, err)
	}
	if err := client.Set(ledgerKey, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectUserLikeStateScript.Run(client, []string{tempSetKey, ledgerKey, lockKey}, userID).Result(); err == nil || !strings.Contains(err.Error(), "LIKE_USER_LEDGER_TYPE") {
		t.Fatalf("wrong Ledger type error=%v", err)
	}
	if err := client.Del(ledgerKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(noSentinelKey, "123").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectUserLikeStateScript.Run(client, []string{noSentinelKey, ledgerKey, lockKey}, userID).Result(); err == nil || !strings.Contains(err.Error(), "LIKE_USER_NOT_READY") {
		t.Fatalf("missing sentinel was accepted error=%v", err)
	}
}

func TestUserLikeRestoreInstallRequiresCompleteSetAndFencesLateOwnerIntegration(t *testing.T) {
	settings := userLikeIntegrationSettings(true, time.Hour)
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, settings)
	ctx := context.Background()
	expectedExpiry := strconv.FormatInt(time.Now().Add(-time.Second).UnixMilli(), 10)
	if err := client.HSet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10), expectedExpiry).Err(); err != nil {
		t.Fatal(err)
	}
	firstStatus, expected, err := store.beginUserLikeRestore(ctx, userID, "owner-a", 300*time.Millisecond)
	if err != nil || firstStatus != "acquired" || expected != expectedExpiry {
		t.Fatalf("first lock status=%q expiry=%q err=%v", firstStatus, expected, err)
	}
	tempA, err := store.createUserLikeRestoreTemp(ctx, userID, "owner-a", expected, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.releaseUserLikeRestore(ctx, userID, "owner-a", tempA)
	if status, _, err := store.beginUserLikeRestore(ctx, userID, "owner-b", time.Minute); err != nil || status != "busy" {
		t.Fatalf("second lock status=%q err=%v want busy", status, err)
	}
	if err := store.addUserLikeRestoreTempMembers(ctx, tempA, []uint{postID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.finishUserLikeRestore(ctx, userID, "wrong-token", expectedExpiry, tempA, 1, settings); !errors.Is(err, ErrUserLikeRecoveryLockLost) {
		t.Fatalf("wrong Token finish error=%v want lock lost", err)
	}
	if _, err := store.finishUserLikeRestore(ctx, userID, "owner-a", expectedExpiry, tempA, 2, settings); !errors.Is(err, ErrUserLikeRecoveryIncomplete) {
		t.Fatalf("partial temporary Set finish error=%v want incomplete", err)
	}
	if exists, err := client.Exists(UserLikesKey(userID)).Result(); err != nil || exists != 0 {
		t.Fatalf("partial restore exposed formal Set exists=%d err=%v", exists, err)
	}
	if _, err := store.finishUserLikeRestore(ctx, userID, "owner-a", expectedExpiry, tempA, 1, settings); err != nil {
		t.Fatalf("complete restore install error=%v", err)
	}
	if count, err := client.SCard(UserLikesKey(userID)).Result(); err != nil || count != 2 {
		t.Fatalf("installed Set size=%d err=%v want sentinel + relation", count, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatal(err)
	}

	lateUserID := userID + 3
	lateExpected := strconv.FormatInt(time.Now().Add(-time.Second).UnixMilli(), 10)
	if err := client.HSet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(lateUserID), 10), lateExpected).Err(); err != nil {
		t.Fatal(err)
	}
	status, lateExpiry, err := store.beginUserLikeRestore(ctx, lateUserID, "late-a", 80*time.Millisecond)
	if err != nil || status != "acquired" {
		t.Fatalf("late owner A status=%q err=%v", status, err)
	}
	lateTempA, err := store.createUserLikeRestoreTemp(ctx, lateUserID, "late-a", lateExpiry, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	status, lateExpiryB, err := store.beginUserLikeRestore(ctx, lateUserID, "late-b", time.Second)
	if err != nil || status != "acquired" || lateExpiryB != lateExpiry {
		t.Fatalf("late owner B status=%q expiry=%q err=%v", status, lateExpiryB, err)
	}
	lateTempB, err := store.createUserLikeRestoreTemp(ctx, lateUserID, "late-b", lateExpiryB, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	winnerPostID := postID + 1
	if err := store.addUserLikeRestoreTempMembers(ctx, lateTempB, []uint{winnerPostID}); err != nil {
		t.Fatal(err)
	}
	noArming := settings
	noArming.ArmingEnabled = false
	if _, err := store.finishUserLikeRestore(ctx, lateUserID, "late-b", lateExpiryB, lateTempB, 1, noArming); err != nil {
		t.Fatalf("late owner B install error=%v", err)
	}
	if status, err := store.finishUserLikeRestore(ctx, lateUserID, "late-a", lateExpiry, lateTempA, 0, settings); err != nil || status != "ready" {
		t.Fatalf("expired owner A finish status=%q err=%v want safe no-op after B installed", status, err)
	}
	if member, err := client.SIsMember(UserLikesKey(lateUserID), strconv.FormatUint(uint64(winnerPostID), 10)).Result(); err != nil || !member {
		t.Fatalf("late owner A overwrote B's installed relation member=%t err=%v", member, err)
	}

	deletedUserID := lateUserID + 1
	deletedExpiry := strconv.FormatInt(time.Now().Add(-time.Second).UnixMilli(), 10)
	if err := client.HSet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(deletedUserID), 10), deletedExpiry).Err(); err != nil {
		t.Fatal(err)
	}
	status, deletedExpected, err := store.beginUserLikeRestore(ctx, deletedUserID, "deleted-post-owner", time.Second)
	if err != nil || status != "acquired" {
		t.Fatalf("deleted Post restore lock status=%q err=%v", status, err)
	}
	deletedTemp, err := store.createUserLikeRestoreTemp(ctx, deletedUserID, "deleted-post-owner", deletedExpected, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.releaseUserLikeRestore(ctx, deletedUserID, "deleted-post-owner", deletedTemp)
	if err := store.addUserLikeRestoreTempMembers(ctx, deletedTemp, []uint{postID}); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ReadyKey(postID), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.finishUserLikeRestore(ctx, deletedUserID, "deleted-post-owner", deletedExpected, deletedTemp, 1, settings); !errors.Is(err, ErrUserLikeRecoveryUnsafe) {
		t.Fatalf("restore with a deleted Post fence error=%v want unsafe", err)
	}
	if exists, err := client.Exists(UserLikesKey(deletedUserID)).Result(); err != nil || exists != 0 {
		t.Fatalf("deleted Post restore published formal Set exists=%d err=%v", exists, err)
	}
	_ = client.Del(ReadyKey(postID)).Err()
}

func TestUserLikeGetAndGetManyNeverReturnFalseAcrossSetExpiryIntegration(t *testing.T) {
	settings := userLikeIntegrationSettings(true, time.Second)
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, settings)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.PExpire(UserLikesKey(userID), 30*time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	for range 20 {
		state, err := store.Get(ctx, userID, postID)
		if err != nil {
			if !errors.Is(err, ErrUserLikeNotReady) {
				t.Fatalf("Get expiry race error=%v", err)
			}
		} else if !state.Liked {
			t.Fatal("Get returned liked=false after relation had been present before expiry")
		}
		states, _, err := store.GetMany(ctx, userID, []uint{postID})
		if err != nil {
			if !errors.Is(err, ErrUserLikeNotReady) {
				t.Fatalf("GetMany expiry race error=%v", err)
			}
		} else if !states[postID].Liked {
			t.Fatal("GetMany returned liked=false after relation had been present before expiry")
		}
		time.Sleep(time.Millisecond)
	}
}

func userLikeIntegrationSettings(arming bool, ttl time.Duration) config.UserLikeLifecycleConfig {
	return config.UserLikeLifecycleConfig{
		ArmingEnabled: arming, RestoreEnabled: true, SetTTL: ttl,
		RestoreLockTTL: 5 * time.Second, RestoreBatchSize: 2,
		RestoreMaxRelations: 100, RestoreRequestTimeout: 3 * time.Second,
		RestoreConcurrency: 4,
	}
}

func openUserLikeLifecycleRedisIntegration(t *testing.T, settings config.UserLikeLifecycleConfig) (*redis.Client, *Store, uint, uint) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis User Like lifecycle integration tests")
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		_ = client.Close()
		t.Fatalf("connect to Redis integration service: %v", err)
	}
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1000000
	t.Cleanup(func() {
		cleanupUserLikeLifecycleRedisState(client, userID, postID)
		_ = client.Close()
	})
	return client, NewStoreWithUserLikeSettings(client, settings), userID, postID
}

func cleanupUserLikeLifecycleRedisState(client *redis.Client, userID, postID uint) {
	if client == nil {
		return
	}
	uid := strconv.FormatUint(uint64(userID), 10)
	post := strconv.FormatUint(uint64(postID), 10)
	pair := BehaviorPair(userID, postID)
	lateUserID := userID + 3
	deletedUserID := userID + 4
	_ = client.Del(UserLikesKey(userID), UserLikesKey(lateUserID), UserLikesKey(deletedUserID), UserLikesRestoreLockKey(userID), UserLikesRestoreLockKey(lateUserID), UserLikesRestoreLockKey(deletedUserID),
		UserLikesRestoreTempKey(userID, "owner-a"), UserLikesRestoreTempKey(lateUserID, "late-a"), UserLikesRestoreTempKey(lateUserID, "late-b"), UserLikesRestoreTempKey(deletedUserID, "deleted-post-owner")).Err()
	_ = client.HDel(UserLikesExpiryLedgerKey, uid, strconv.FormatUint(uint64(userID+1), 10), strconv.FormatUint(uint64(userID+2), 10), strconv.FormatUint(uint64(lateUserID), 10), strconv.FormatUint(uint64(deletedUserID), 10)).Err()
	_ = client.Del(ReadyKey(postID), CountKey(postID), VersionKey(postID), RebuildTokenKey(postID)).Err()
	_ = client.SRem(DirtyKey, post).Err()
	_ = client.SRem(RegistryKey, post).Err()
	_ = client.ZRem(ExpiryCandidatesKey, post).Err()
	_ = client.HDel(RecoverableVersionsKey, post).Err()
	_ = client.SRem(BehaviorDirtyKey, pair).Err()
	_ = client.HDel(BehaviorStateKey, pair).Err()
	_ = client.ZRem(BehaviorProcessingKey, pair).Err()
	_ = client.HDel(BehaviorClaimsKey, pair).Err()
}

func assertUserLikeTTLAndLedgerMatch(t *testing.T, client *redis.Client, userID uint) error {
	t.Helper()
	result, err := client.Eval(`
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
return {redis.call('PTTL', KEYS[1]), redis.call('HGET', KEYS[2], ARGV[1]), now_ms}
`, []string{UserLikesKey(userID), UserLikesExpiryLedgerKey}, strconv.FormatUint(uint64(userID), 10)).Result()
	if err != nil {
		return err
	}
	items, ok := result.([]interface{})
	if !ok || len(items) != 3 {
		return fmt.Errorf("unexpected TTL and Ledger check result %T", result)
	}
	pttl, _ := strconv.ParseInt(asString(items[0]), 10, 64)
	expiresAt, parseErr := strconv.ParseInt(asString(items[1]), 10, 64)
	nowMS, _ := strconv.ParseInt(asString(items[2]), 10, 64)
	if pttl <= 0 || parseErr != nil || expiresAt <= 0 || absInt64((nowMS+pttl)-expiresAt) > 2 {
		return fmt.Errorf("User Set and Ledger expiry diverged: pttl_ms=%d expiry=%d redis_now_ms=%d", pttl, expiresAt, nowMS)
	}
	return nil
}

func readUserLikeLedgerExpiry(t *testing.T, client *redis.Client, userID uint) int64 {
	t.Helper()
	value, err := client.HGet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse User Like Ledger expiry %q: %v", value, err)
	}
	return expiresAt
}

func assertPersistentPostLikeKeys(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	for _, key := range []string{ReadyKey(postID), CountKey(postID), VersionKey(postID)} {
		if ttl, err := client.PTTL(key).Result(); err != nil || ttl >= 0 {
			t.Fatalf("Post key %q TTL=%s err=%v want persistent", key, ttl, err)
		}
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("Post key %q is not present exists=%d err=%v", key, exists, err)
		}
	}
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
