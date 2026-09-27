package likes

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestStoreMutationAndClaimOwnershipIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	const userID uint = 11
	pair := BehaviorPair(userID, postID)
	ctx := context.Background()
	cleanup := func() {
		client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
		client.SRem(DirtyKey, postID)
		client.ZRem(ProcessingKey, postID)
		client.HDel(ClaimsKey, strconv.FormatUint(uint64(postID), 10))
		client.SRem(RegistryKey, postID)
		client.ZRem(ExpiryCandidatesKey, postID)
		client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10))
		client.SRem(BehaviorDirtyKey, pair)
		client.HDel(BehaviorStateKey, pair)
		client.ZRem(BehaviorProcessingKey, pair)
		client.HDel(BehaviorClaimsKey, pair)
	}
	cleanup()
	defer cleanup()
	created, err := store.Initialize(ctx, postID, 0, 0, nil)
	if err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	first, err := store.Mutate(ctx, userID, postID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Liked || first.Count != 1 || first.Version != 1 {
		t.Fatalf("first mutation=%+v", first)
	}
	duplicate, err := store.Mutate(ctx, userID, postID, true)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Changed || duplicate.Count != 1 || duplicate.Version != 1 {
		t.Fatalf("duplicate mutation=%+v", duplicate)
	}
	claims, err := store.ClaimDirty(ctx, 100, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok := findClaim(claims, postID)
	if !ok {
		t.Fatalf("article claim missing: %+v", claims)
	}
	if ok, err := store.RequeueClaim(ctx, claim); err != nil || !ok {
		t.Fatalf("requeue ok=%t err=%v", ok, err)
	}
	claims, err = store.ClaimDirty(ctx, 100, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	newClaim, ok := findClaim(claims, postID)
	if !ok {
		t.Fatalf("replacement claim missing: %+v", claims)
	}
	if acked, err := store.AckClaim(ctx, claim); err != nil || acked {
		t.Fatalf("stale ACK acked=%t err=%v", acked, err)
	}
	if acked, err := store.AckClaim(ctx, newClaim); err != nil || !acked {
		t.Fatalf("current ACK acked=%t err=%v", acked, err)
	}
	last, err := store.Mutate(ctx, userID, postID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !last.Changed || last.Liked || last.Count != 0 || last.Version != 2 {
		t.Fatalf("unlike=%+v", last)
	}
}

func TestStoreGetManyIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(client)
	base := uint(time.Now().UnixNano() & 0x3fffffff)
	postIDs := []uint{base, base + 1, base + 2}
	ctx := context.Background()
	cleanup := func() {
		for _, postID := range postIDs {
			client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
			client.SRem(DirtyKey, postID)
			client.ZRem(ProcessingKey, postID)
			client.HDel(ClaimsKey, strconv.FormatUint(uint64(postID), 10))
			client.SRem(RegistryKey, postID)
			client.ZRem(ExpiryCandidatesKey, postID)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10))
		}
	}
	cleanup()
	defer cleanup()

	if created, err := store.Initialize(ctx, postIDs[0], 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("article A initialize created=%t err=%v", created, err)
	}
	if created, err := store.Initialize(ctx, postIDs[1], 0, 0, nil); err != nil || !created {
		t.Fatalf("article B initialize created=%t err=%v", created, err)
	}

	states, unavailable, err := store.GetMany(ctx, 11, postIDs)
	if err != nil {
		t.Fatal(err)
	}
	if states[postIDs[0]].Count != 1 || !states[postIDs[0]].Liked {
		t.Fatalf("article A state=%+v", states[postIDs[0]])
	}
	if states[postIDs[1]].Count != 0 || states[postIDs[1]].Liked {
		t.Fatalf("article B state=%+v", states[postIDs[1]])
	}
	if !equalUintSlices(unavailable, []uint{postIDs[2]}) {
		t.Fatalf("unavailable=%v", unavailable)
	}
}

func TestStorePurgePostRemovesOnlyTargetLikeStateIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}

	store := NewStore(client)
	target := uint(time.Now().UnixNano() & 0x3fffffff)
	unrelated := target + 1
	userID := uint(23)
	targetPair := BehaviorPair(userID, target)
	unrelatedPair := BehaviorPair(userID, unrelated)
	ctx := context.Background()
	cleanup := func() {
		for _, postID := range []uint{target, unrelated} {
			client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
			client.SRem(DirtyKey, postID)
			client.ZRem(ProcessingKey, postID)
			client.HDel(ClaimsKey, strconv.FormatUint(uint64(postID), 10))
			client.SRem(RegistryKey, postID)
			client.ZRem(ExpiryCandidatesKey, postID)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10))
		}
		for _, pair := range []string{targetPair, unrelatedPair} {
			client.SRem(BehaviorDirtyKey, pair)
			client.HDel(BehaviorStateKey, pair)
			client.ZRem(BehaviorProcessingKey, pair)
			client.HDel(BehaviorClaimsKey, pair)
		}
	}
	cleanup()
	defer cleanup()

	for _, postID := range []uint{target, unrelated} {
		if created, err := store.Initialize(ctx, postID, 1, 7, []uint{userID}); err != nil || !created {
			t.Fatalf("initialize post=%d created=%t err=%v", postID, created, err)
		}
	}
	client.SAdd(DirtyKey, target, unrelated)
	client.ZAdd(ProcessingKey, &redis.Z{Score: 1, Member: target}, &redis.Z{Score: 2, Member: unrelated})
	client.HSet(ClaimsKey, strconv.FormatUint(uint64(target), 10), "target-claim", strconv.FormatUint(uint64(unrelated), 10), "unrelated-claim")
	client.SAdd(BehaviorDirtyKey, targetPair, unrelatedPair)
	client.HSet(BehaviorStateKey, targetPair, "target-behavior", unrelatedPair, "unrelated-behavior")
	client.ZAdd(BehaviorProcessingKey, &redis.Z{Score: 1, Member: targetPair}, &redis.Z{Score: 2, Member: unrelatedPair})
	client.HSet(BehaviorClaimsKey, targetPair, "target-behavior-claim", unrelatedPair, "unrelated-behavior-claim")

	if err := store.PurgePost(ctx, target); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{ReadyKey(target), CountKey(target), UsersKey(target), VersionKey(target)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("target key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if member, err := client.SIsMember(DirtyKey, target).Result(); err != nil || member {
		t.Fatalf("target dirty member=%t err=%v", member, err)
	}
	if _, err := client.ZScore(ProcessingKey, strconv.FormatUint(uint64(target), 10)).Result(); err != redis.Nil {
		t.Fatalf("target processing score err=%v", err)
	}
	if exists, err := client.HExists(ClaimsKey, strconv.FormatUint(uint64(target), 10)).Result(); err != nil || exists {
		t.Fatalf("target claim exists=%t err=%v", exists, err)
	}

	for _, key := range []string{ReadyKey(unrelated), CountKey(unrelated), UsersKey(unrelated), VersionKey(unrelated)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("unrelated key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if member, err := client.SIsMember(DirtyKey, unrelated).Result(); err != nil || !member {
		t.Fatalf("unrelated dirty member=%t err=%v", member, err)
	}
	if _, err := client.ZScore(ProcessingKey, strconv.FormatUint(uint64(unrelated), 10)).Result(); err != nil {
		t.Fatalf("unrelated processing err=%v", err)
	}
	if exists, err := client.HExists(ClaimsKey, strconv.FormatUint(uint64(unrelated), 10)).Result(); err != nil || !exists {
		t.Fatalf("unrelated claim exists=%t err=%v", exists, err)
	}
	for _, check := range []struct {
		key   string
		field string
	}{
		{BehaviorStateKey, targetPair}, {BehaviorStateKey, unrelatedPair},
		{BehaviorClaimsKey, targetPair}, {BehaviorClaimsKey, unrelatedPair},
	} {
		if exists, err := client.HExists(check.key, check.field).Result(); err != nil || !exists {
			t.Fatalf("behavior key=%q field=%q exists=%t err=%v", check.key, check.field, exists, err)
		}
	}
}

func equalUintSlices(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func findClaim(claims []SnapshotClaim, postID uint) (SnapshotClaim, bool) {
	for _, claim := range claims {
		if claim.PostID == postID {
			return claim, true
		}
	}
	return SnapshotClaim{}, false
}
