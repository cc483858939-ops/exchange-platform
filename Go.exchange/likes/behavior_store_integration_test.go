package likes

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestBehaviorClaimsAreOwnedAndVersionAwareIntegration(t *testing.T) {
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
	ctx := context.Background()
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1
	pair := BehaviorPair(userID, postID)
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
	for version := 1; version <= 100; version++ {
		result, err := store.Mutate(ctx, userID, postID, version%2 == 1)
		if err != nil {
			t.Fatal(err)
		}
		if result.Version != int64(version) {
			t.Fatalf("version=%d want=%d", result.Version, version)
		}
	}
	if dirty, err := client.SIsMember(BehaviorDirtyKey, pair).Result(); err != nil || !dirty {
		t.Fatalf("dirty=%t err=%v", dirty, err)
	}
	state, err := client.HGet(BehaviorStateKey, pair).Result()
	if err != nil {
		t.Fatal(err)
	}
	liked, version, _, err := parseBehaviorState(state)
	if err != nil || liked || version != 100 {
		t.Fatalf("state=%q liked=%t version=%d err=%v", state, liked, version, err)
	}

	firstClaims, err := store.ClaimBehaviorDirty(ctx, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := findBehaviorClaim(firstClaims, pair)
	if !ok {
		t.Fatalf("first claim missing: %+v", firstClaims)
	}
	firstDeliveries, err := store.LoadBehaviorDeliveries(ctx, []BehaviorClaim{first})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RequeueBehaviorClaims(ctx, []BehaviorClaim{first}); err != nil {
		t.Fatal(err)
	}
	secondClaims, err := store.ClaimBehaviorDirty(ctx, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second, ok := findBehaviorClaim(secondClaims, pair)
	if !ok {
		t.Fatalf("second claim missing: %+v", secondClaims)
	}
	if acked, err := store.AckBehaviorDeliveries(ctx, firstDeliveries); err != nil || acked != 0 {
		t.Fatalf("stale claim acked=%d err=%v", acked, err)
	}

	secondDeliveries, err := store.LoadBehaviorDeliveries(ctx, []BehaviorClaim{second})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := store.Mutate(ctx, userID, postID, true)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 101 {
		t.Fatalf("latest=%+v", latest)
	}
	if acked, err := store.AckBehaviorDeliveries(ctx, secondDeliveries); err != nil || acked != 1 {
		t.Fatalf("old version conditional ACK=%d err=%v", acked, err)
	}
	if dirty, _ := client.SIsMember(BehaviorDirtyKey, pair).Result(); !dirty {
		t.Fatal("newer state was lost after old version ACK")
	}
	if state, err := client.HGet(BehaviorStateKey, pair).Result(); err != nil {
		t.Fatal(err)
	} else if _, version, _, err := parseBehaviorState(state); err != nil || version != 101 {
		t.Fatalf("latest state=%q version=%d err=%v", state, version, err)
	}
}

func findBehaviorClaim(claims []BehaviorClaim, pair string) (BehaviorClaim, bool) {
	for _, claim := range claims {
		if claim.Pair == pair {
			return claim, true
		}
	}
	return BehaviorClaim{}, false
}
