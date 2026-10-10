package likes

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/go-redis/redis/v7"
)

func TestUserLikeCapEvictsOldestAndFailsClosedOnInvalidStateIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run User Like cap integration test")
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	baseID := uint(time.Now().UnixNano() & 0x0fffffff)
	userID := baseID + 500000000
	postIDs := make([]uint, config.DefaultUserLikeRestoreMaxRelations)
	for index := range postIDs {
		postIDs[index] = baseID + uint(index) + 1
	}
	targetID := baseID + uint(len(postIDs)) + 1
	staleTargetID := targetID + 1
	overCapTargetID := targetID + 2
	legacyUserID := userID + 1
	legacyRelationID := targetID + 3
	legacyTargetID := targetID + 4
	overCapRelationID := targetID + 5
	allPostIDs := append(append([]uint(nil), postIDs...), targetID, staleTargetID, overCapTargetID, legacyRelationID, legacyTargetID, overCapRelationID)
	t.Cleanup(func() {
		cleanupUserLikeCapFixture(client, userID, legacyUserID, allPostIDs)
		_ = client.Close()
	})
	cleanupUserLikeCapFixture(client, userID, legacyUserID, allPostIDs)
	store := NewStoreWithUserLikeSettings(client, config.UserLikeLifecycleConfig{
		ArmingEnabled: false, RestoreEnabled: true, SetTTL: 72 * time.Hour,
		RestoreMaxRelations: config.DefaultUserLikeRestoreMaxRelations,
	})
	ctx := context.Background()
	if err := client.Eval(`
redis.call('SADD', KEYS[1], '0')
for index, post_id in ipairs(ARGV) do
  redis.call('SADD', KEYS[1], post_id)
  redis.call('ZADD', KEYS[2], index, post_id)
  redis.call('SET', 'post:like:' .. post_id .. ':ready', '1')
  redis.call('SET', 'post:like:' .. post_id .. ':count', '1')
  redis.call('SET', 'post:like:' .. post_id .. ':version', '1')
  redis.call('SADD', KEYS[3], post_id)
end
return #ARGV
`, []string{UserLikesKey(userID), UserLikesOrderKey(userID), RegistryKey}, formatPostIDs(postIDs)...).Err(); err != nil {
		t.Fatal(err)
	}
	for _, postID := range []uint{targetID, staleTargetID, overCapTargetID, legacyTargetID} {
		if err := client.Set(ReadyKey(postID), "1", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(CountKey(postID), "0", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(VersionKey(postID), "0", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	result, err := store.Mutate(ctx, userID, targetID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Liked || result.Count != 1 || result.Version != 1 || result.EvictedPostID != postIDs[0] || result.ActiveRelations != 10000 {
		t.Fatalf("first cap mutation=%+v, want oldest relation replaced at 10,000", result)
	}
	assertUserLikeCapMutationState(t, client, userID, postIDs[0], targetID, 0, 2, "0|2|", "1|1|")

	if err := client.Set(ReadyKey(postIDs[1]), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(CountKey(postIDs[1]), VersionKey(postIDs[1])).Err(); err != nil {
		t.Fatal(err)
	}
	staleResult, err := store.Mutate(ctx, userID, staleTargetID, true)
	if err != nil {
		t.Fatal(err)
	}
	if staleResult.EvictedPostID != postIDs[1] || staleResult.ActiveRelations != 10000 {
		t.Fatalf("deleted-candidate mutation=%+v, want stale oldest relation reclaimed", staleResult)
	}
	if member, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postIDs[1]), 10)).Result(); err != nil || member {
		t.Fatalf("deleted candidate remains in User Set=%t err=%v", member, err)
	}
	if score, err := client.ZScore(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postIDs[1]), 10)).Result(); err != redis.Nil || score != 0 {
		t.Fatalf("deleted candidate remains in Order ZSET score=%v err=%v", score, err)
	}
	if dirty, err := client.SIsMember(DirtyKey, strconv.FormatUint(uint64(postIDs[1]), 10)).Result(); err != nil || dirty {
		t.Fatalf("deleted candidate recreated Post dirty state=%t err=%v", dirty, err)
	}

	if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(overCapRelationID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(UserLikesOrderKey(userID), &redis.Z{Score: 20000, Member: strconv.FormatUint(uint64(overCapRelationID), 10)}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, userID, overCapTargetID, true); !errors.Is(err, ErrUserLikeOverCap) {
		t.Fatalf("over-cap state error=%v, want fail-closed ErrUserLikeOverCap", err)
	}
	if count, err := client.Get(CountKey(overCapTargetID)).Result(); err != nil || count != "0" {
		t.Fatalf("over-cap rejection changed target count=%q err=%v", count, err)
	}

	if err := client.SAdd(UserLikesKey(legacyUserID), UserLikesInitSentinel, strconv.FormatUint(uint64(legacyRelationID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(CountKey(legacyRelationID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(VersionKey(legacyRelationID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, legacyUserID, legacyTargetID, true); !errors.Is(err, ErrUserLikeOrderIndexMissing) {
		t.Fatalf("Set without Order ZSET error=%v, want fail-closed missing index", err)
	}
	if count, err := client.Get(CountKey(legacyTargetID)).Result(); err != nil || count != "0" {
		t.Fatalf("old-format relation rejection changed target count=%q err=%v", count, err)
	}
}

func TestUserLikeCapLifecycleWithInjectedSmallLimitIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run User Like cap lifecycle integration test")
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	baseID := uint(time.Now().UnixNano() & 0x0fffffff)
	userID := baseID + 500000000
	cleanupUserID := userID + 1
	postIDs := make([]uint, 24)
	for index := range postIDs {
		postIDs[index] = baseID + uint(index) + 1
	}
	t.Cleanup(func() {
		cleanupUserLikeCapFixture(client, userID, cleanupUserID, postIDs)
		_ = client.HDel(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10), strconv.FormatUint(uint64(cleanupUserID), 10)).Err()
		_ = client.Close()
	})
	cleanupUserLikeCapFixture(client, userID, cleanupUserID, postIDs)
	store := NewStoreWithUserLikeSettingsAndRelationLimit(client, config.UserLikeLifecycleConfig{
		ArmingEnabled: true, RestoreEnabled: true, SetTTL: 72 * time.Hour,
		RestoreMaxRelations: 3,
	}, 3)
	ctx := context.Background()
	for _, postID := range postIDs {
		if _, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil {
			t.Fatalf("initialize Post %d: %v", postID, err)
		}
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}
	initialMembers, err := client.SMembers(UserLikesKey(userID)).Result()
	if err != nil || len(initialMembers) != 1 || initialMembers[0] != UserLikesInitSentinel {
		t.Fatalf("new User Set members=%v err=%v, want only sentinel", initialMembers, err)
	}
	if exists, err := client.Exists(UserLikesOrderKey(userID)).Result(); err != nil || exists != 0 {
		t.Fatalf("new empty User Order ZSET exists=%d err=%v, want absent", exists, err)
	}
	initialTTL, err := client.PTTL(UserLikesKey(userID)).Result()
	if err != nil || initialTTL <= 0 || initialTTL > 72*time.Hour {
		t.Fatalf("new User Set TTL=%s err=%v, want active 72h TTL", initialTTL, err)
	}

	for _, postID := range postIDs[:3] {
		if result, err := store.Mutate(ctx, userID, postID, true); err != nil || !result.Changed {
			t.Fatalf("initial Like Post %d result=%+v err=%v", postID, result, err)
		}
		time.Sleep(time.Millisecond)
	}
	if card, err := client.SCard(UserLikesKey(userID)).Result(); err != nil || card != 4 {
		t.Fatalf("initial User Set cardinality=%d err=%v, want sentinel + 3", card, err)
	}
	if card, err := client.ZCard(UserLikesOrderKey(userID)).Result(); err != nil || card != 3 {
		t.Fatalf("initial Order ZSET cardinality=%d err=%v, want 3", card, err)
	}

	firstEviction, err := store.Mutate(ctx, userID, postIDs[3], true)
	if err != nil || firstEviction.EvictedPostID != postIDs[0] {
		t.Fatalf("first eviction result=%+v err=%v, want oldest Post %d", firstEviction, err, postIDs[0])
	}
	assertSmallCapPostState(t, client, userID, postIDs[0], false, 0, 2, "0|2|")
	assertSmallCapPostState(t, client, userID, postIDs[3], true, 1, 1, "1|1|")

	time.Sleep(time.Millisecond)
	relike, err := store.Mutate(ctx, userID, postIDs[0], true)
	if err != nil || relike.EvictedPostID != postIDs[1] {
		t.Fatalf("re-Like evicted relation result=%+v err=%v, want oldest remaining Post %d", relike, err, postIDs[1])
	}
	assertSmallCapPostState(t, client, userID, postIDs[1], false, 0, 2, "0|2|")
	assertSmallCapPostState(t, client, userID, postIDs[0], true, 1, 3, "1|3|")

	orderedBefore, err := client.ZScore(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postIDs[2]), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	ledgerBefore, err := client.HGet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	duplicate, err := store.Mutate(ctx, userID, postIDs[2], true)
	if err != nil || duplicate.Changed || duplicate.EvictedPostID != 0 {
		t.Fatalf("idempotent Like result=%+v err=%v", duplicate, err)
	}
	orderedAfter, err := client.ZScore(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postIDs[2]), 10)).Result()
	if err != nil || orderedAfter != orderedBefore {
		t.Fatalf("idempotent Like changed order score from %v to %v err=%v", orderedBefore, orderedAfter, err)
	}
	ledgerAfter, err := client.HGet(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	beforeExpiry, beforeErr := strconv.ParseInt(ledgerBefore, 10, 64)
	afterExpiry, afterErr := strconv.ParseInt(ledgerAfter, 10, 64)
	if beforeErr != nil || afterErr != nil || afterExpiry <= beforeExpiry {
		t.Fatalf("idempotent Like did not refresh ledger deadline: before=%q after=%q errors=%v/%v", ledgerBefore, ledgerAfter, beforeErr, afterErr)
	}
	setTTL, setErr := client.PTTL(UserLikesKey(userID)).Result()
	orderTTL, orderErr := client.PTTL(UserLikesOrderKey(userID)).Result()
	if setErr != nil || orderErr != nil || absDuration(setTTL-orderTTL) > time.Millisecond {
		t.Fatalf("Set/Order TTL mismatch after idempotent Like: Set=%s/%v Order=%s/%v", setTTL, setErr, orderTTL, orderErr)
	}

	unlike, err := store.Mutate(ctx, userID, postIDs[2], false)
	if err != nil || !unlike.Changed || unlike.Liked || unlike.Count != 0 || unlike.Version != 2 {
		t.Fatalf("Unlike result=%+v err=%v", unlike, err)
	}
	assertSmallCapPostState(t, client, userID, postIDs[2], false, 0, 2, "0|2|")
	if setMember, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postIDs[2]), 10)).Result(); err != nil || setMember {
		t.Fatalf("Unlike retained Set member=%t err=%v", setMember, err)
	}
	if score, err := client.ZScore(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postIDs[2]), 10)).Result(); err != redis.Nil || score != 0 {
		t.Fatalf("Unlike retained Order member score=%v err=%v", score, err)
	}

	var wait sync.WaitGroup
	mutationErrors := make(chan error, len(postIDs)-4)
	for _, postID := range postIDs[4:] {
		wait.Add(1)
		go func(postID uint) {
			defer wait.Done()
			result, mutateErr := store.Mutate(ctx, userID, postID, true)
			if mutateErr != nil {
				mutationErrors <- mutateErr
				return
			}
			if !result.Changed || !result.Liked {
				mutationErrors <- errors.New("concurrent Like was unexpectedly idempotent")
			}
		}(postID)
	}
	wait.Wait()
	close(mutationErrors)
	for err := range mutationErrors {
		t.Errorf("concurrent Like: %v", err)
	}
	setMembers, err := client.SMembers(UserLikesKey(userID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	orderMembers, err := client.ZRange(UserLikesOrderKey(userID), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(setMembers) != 4 || len(orderMembers) != 3 {
		t.Fatalf("concurrent Like cardinalities Set=%d Order=%d, want 4/3", len(setMembers), len(orderMembers))
	}
	setRelations := make(map[string]bool, len(setMembers))
	for _, member := range setMembers {
		if member != UserLikesInitSentinel {
			setRelations[member] = true
		}
	}
	for _, member := range orderMembers {
		if !setRelations[member] {
			t.Fatalf("Order member %q is absent from User Set", member)
		}
		delete(setRelations, member)
	}
	if len(setRelations) != 0 {
		t.Fatalf("User Set has relations absent from Order ZSET: %v", setRelations)
	}
	for _, postID := range postIDs {
		count, countErr := client.Get(CountKey(postID)).Int64()
		if countErr != nil || count < 0 {
			t.Fatalf("Post %d Count=%d err=%v, want nonnegative", postID, count, countErr)
		}
		member := strconv.FormatUint(uint64(postID), 10)
		if (count == 1) != setContains(setMembers, member) {
			t.Fatalf("Post %d Count=%d disagrees with User Set membership", postID, count)
		}
	}
}

func assertSmallCapPostState(t *testing.T, client *redis.Client, userID, postID uint, liked bool, count, version int64, behaviorPrefix string) {
	t.Helper()
	postIDString := strconv.FormatUint(uint64(postID), 10)
	member, err := client.SIsMember(UserLikesKey(userID), postIDString).Result()
	if err != nil || member != liked {
		t.Fatalf("Post %d User Set member=%t err=%v, want liked=%t", postID, member, err, liked)
	}
	_, scoreErr := client.ZScore(UserLikesOrderKey(userID), postIDString).Result()
	ordered := scoreErr == nil
	if (scoreErr != nil && scoreErr != redis.Nil) || ordered != liked {
		t.Fatalf("Post %d Order member exists=%t err=%v, want liked=%t", postID, ordered, scoreErr, liked)
	}
	actualCount, countErr := client.Get(CountKey(postID)).Int64()
	actualVersion, versionErr := client.Get(VersionKey(postID)).Int64()
	if countErr != nil || versionErr != nil || actualCount != count || actualVersion != version {
		t.Fatalf("Post %d aggregate count=%d/%v version=%d/%v want %d/%d", postID, actualCount, countErr, actualVersion, versionErr, count, version)
	}
	state, stateErr := client.HGet(BehaviorStateKey, BehaviorPair(userID, postID)).Result()
	if stateErr != nil || !strings.HasPrefix(state, behaviorPrefix) {
		t.Fatalf("Post %d Behavior state=%q err=%v want prefix %q", postID, state, stateErr, behaviorPrefix)
	}
	pair := BehaviorPair(userID, postID)
	if dirty, dirtyErr := client.SIsMember(BehaviorDirtyKey, pair).Result(); dirtyErr != nil || !dirty {
		t.Fatalf("Post %d Behavior Dirty=%t err=%v", postID, dirty, dirtyErr)
	}
	if dirty, dirtyErr := client.SIsMember(DirtyKey, postIDString).Result(); dirtyErr != nil || !dirty {
		t.Fatalf("Post %d Snapshot Dirty=%t err=%v", postID, dirty, dirtyErr)
	}
}

func setContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func assertUserLikeCapMutationState(t *testing.T, client *redis.Client, userID, evictedPostID, targetPostID uint, evictedCount, evictedVersion int64, evictedBehaviorPrefix, targetBehaviorPrefix string) {
	t.Helper()
	if card, err := client.SCard(UserLikesKey(userID)).Result(); err != nil || card != 10001 {
		t.Fatalf("User Set cardinality=%d err=%v want sentinel + 10,000 relations", card, err)
	}
	if card, err := client.ZCard(UserLikesOrderKey(userID)).Result(); err != nil || card != 10000 {
		t.Fatalf("Order ZSET cardinality=%d err=%v want 10,000", card, err)
	}
	count, err := client.Get(CountKey(evictedPostID)).Int64()
	version, versionErr := client.Get(VersionKey(evictedPostID)).Int64()
	if err != nil || versionErr != nil || count != evictedCount || version != evictedVersion {
		t.Fatalf("evicted Post aggregate count=%d/%v version=%d/%v", count, err, version, versionErr)
	}
	for _, test := range []struct {
		postID uint
		prefix string
	}{
		{postID: evictedPostID, prefix: evictedBehaviorPrefix},
		{postID: targetPostID, prefix: targetBehaviorPrefix},
	} {
		pair := BehaviorPair(userID, test.postID)
		state, err := client.HGet(BehaviorStateKey, pair).Result()
		if err != nil || len(state) < len(test.prefix) || state[:len(test.prefix)] != test.prefix {
			t.Fatalf("Post %d Behavior state=%q err=%v want prefix %q", test.postID, state, err, test.prefix)
		}
		if dirty, err := client.SIsMember(BehaviorDirtyKey, pair).Result(); err != nil || !dirty {
			t.Fatalf("Post %d Behavior Dirty=%t err=%v", test.postID, dirty, err)
		}
		if dirty, err := client.SIsMember(DirtyKey, strconv.FormatUint(uint64(test.postID), 10)).Result(); err != nil || !dirty {
			t.Fatalf("Post %d Snapshot Dirty=%t err=%v", test.postID, dirty, err)
		}
	}
}

func formatPostIDs(postIDs []uint) []interface{} {
	args := make([]interface{}, len(postIDs))
	for index, postID := range postIDs {
		args[index] = strconv.FormatUint(uint64(postID), 10)
	}
	return args
}

func cleanupUserLikeCapFixture(client *redis.Client, userID, legacyUserID uint, postIDs []uint) {
	if client == nil {
		return
	}
	keys := make([]string, 0, len(postIDs)*4+4)
	postMembers := make([]interface{}, 0, len(postIDs))
	postFields := make([]string, 0, len(postIDs))
	pairs := make([]string, 0, len(postIDs)*2)
	pairMembers := make([]interface{}, 0, len(postIDs)*2)
	for _, postID := range postIDs {
		post := strconv.FormatUint(uint64(postID), 10)
		keys = append(keys, ReadyKey(postID), CountKey(postID), VersionKey(postID), RebuildTokenKey(postID))
		postMembers = append(postMembers, post)
		postFields = append(postFields, post)
		for _, uid := range []uint{userID, legacyUserID} {
			pair := BehaviorPair(uid, postID)
			pairs = append(pairs, pair)
			pairMembers = append(pairMembers, pair)
		}
	}
	keys = append(keys, UserLikesKey(userID), UserLikesOrderKey(userID), UserLikesKey(legacyUserID), UserLikesOrderKey(legacyUserID))
	_ = client.Del(keys...).Err()
	_ = client.HDel(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10), strconv.FormatUint(uint64(legacyUserID), 10)).Err()
	_ = client.SRem(RegistryKey, postMembers...).Err()
	_ = client.ZRem(ExpiryCandidatesKey, postMembers...).Err()
	_ = client.SRem(DirtyKey, postMembers...).Err()
	_ = client.HDel(RecoverableVersionsKey, postFields...).Err()
	_ = client.SRem(BehaviorDirtyKey, pairMembers...).Err()
	_ = client.HDel(BehaviorStateKey, pairs...).Err()
}
