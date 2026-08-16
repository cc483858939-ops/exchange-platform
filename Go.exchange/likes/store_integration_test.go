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
	articleID := uint(time.Now().UnixNano() & 0x3fffffff)
	const userID uint = 11
	pair := BehaviorPair(userID, articleID)
	ctx := context.Background()
	cleanup := func() {
		client.Del(ReadyKey(articleID), CountKey(articleID), UsersKey(articleID), VersionKey(articleID))
		client.SRem(DirtyKey, articleID)
		client.ZRem(ProcessingKey, articleID)
		client.HDel(ClaimsKey, strconv.FormatUint(uint64(articleID), 10))
		client.SRem(BehaviorDirtyKey, pair)
		client.HDel(BehaviorStateKey, pair)
		client.ZRem(BehaviorProcessingKey, pair)
		client.HDel(BehaviorClaimsKey, pair)
	}
	cleanup()
	defer cleanup()
	created, err := store.Initialize(ctx, articleID, 0, 0, nil)
	if err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	first, err := store.Mutate(ctx, userID, articleID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Liked || first.Count != 1 || first.Version != 1 {
		t.Fatalf("first mutation=%+v", first)
	}
	duplicate, err := store.Mutate(ctx, userID, articleID, true)
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
	claim, ok := findClaim(claims, articleID)
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
	newClaim, ok := findClaim(claims, articleID)
	if !ok {
		t.Fatalf("replacement claim missing: %+v", claims)
	}
	if acked, err := store.AckClaim(ctx, claim); err != nil || acked {
		t.Fatalf("stale ACK acked=%t err=%v", acked, err)
	}
	if acked, err := store.AckClaim(ctx, newClaim); err != nil || !acked {
		t.Fatalf("current ACK acked=%t err=%v", acked, err)
	}
	last, err := store.Mutate(ctx, userID, articleID, false)
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
	articleIDs := []uint{base, base + 1, base + 2}
	ctx := context.Background()
	cleanup := func() {
		for _, articleID := range articleIDs {
			client.Del(ReadyKey(articleID), CountKey(articleID), UsersKey(articleID), VersionKey(articleID))
			client.SRem(DirtyKey, articleID)
			client.ZRem(ProcessingKey, articleID)
			client.HDel(ClaimsKey, strconv.FormatUint(uint64(articleID), 10))
		}
	}
	cleanup()
	defer cleanup()

	if created, err := store.Initialize(ctx, articleIDs[0], 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("article A initialize created=%t err=%v", created, err)
	}
	if created, err := store.Initialize(ctx, articleIDs[1], 0, 0, nil); err != nil || !created {
		t.Fatalf("article B initialize created=%t err=%v", created, err)
	}

	states, unavailable, err := store.GetMany(ctx, 11, articleIDs)
	if err != nil {
		t.Fatal(err)
	}
	if states[articleIDs[0]].Count != 1 || !states[articleIDs[0]].Liked {
		t.Fatalf("article A state=%+v", states[articleIDs[0]])
	}
	if states[articleIDs[1]].Count != 0 || states[articleIDs[1]].Liked {
		t.Fatalf("article B state=%+v", states[articleIDs[1]])
	}
	if !equalUintSlices(unavailable, []uint{articleIDs[2]}) {
		t.Fatalf("unavailable=%v", unavailable)
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
func findClaim(claims []SnapshotClaim, articleID uint) (SnapshotClaim, bool) {
	for _, claim := range claims {
		if claim.ArticleID == articleID {
			return claim, true
		}
	}
	return SnapshotClaim{}, false
}
