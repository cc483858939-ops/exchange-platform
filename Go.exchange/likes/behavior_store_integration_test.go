package likes

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/config"

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
		client.Del(UserLikesKey(userID), UserLikesOrderKey(userID))
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

	created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil)
	if err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatalf("initialize user Like sentinel: %v", err)
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

	first := claimOwnedBehaviorPairForIntegration(t, store, pair)
	firstDeliveries, err := store.LoadBehaviorDeliveries(ctx, []BehaviorClaim{first})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RequeueBehaviorClaims(ctx, []BehaviorClaim{first}); err != nil {
		t.Fatal(err)
	}
	second := claimOwnedBehaviorPairForIntegration(t, store, pair)
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

func claimOwnedBehaviorPairForIntegration(t *testing.T, store *Store, pair string) BehaviorClaim {
	t.Helper()
	for attempt := 0; attempt < 5; attempt++ {
		claims, err := store.ClaimBehaviorDirty(t.Context(), config.MaxLikeBehaviorBatchSize, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		own, found := findBehaviorClaim(claims, pair)
		others := make([]BehaviorClaim, 0, len(claims))
		for _, claim := range claims {
			if claim.Pair != pair {
				others = append(others, claim)
			}
		}
		if err := store.RequeueBehaviorClaims(t.Context(), others); err != nil {
			t.Fatal(err)
		}
		if found {
			return own
		}
	}
	t.Fatalf("own Behavior Pair %q was not claimed after bounded retries", pair)
	return BehaviorClaim{}
}

func TestBehaviorMalformedStateDoesNotBlockHealthyPairIntegration(t *testing.T) {
	client := queueClaimTestClient(t)
	base := uint(time.Now().UnixNano() & 0x3fffffff)
	good := BehaviorPair(base, base+1)
	bad := BehaviorPair(base, base+2)
	badPair := fmt.Sprintf("bad-pair-%d", base)
	validState := fmt.Sprintf("1|3|%d", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro())
	t.Cleanup(func() {
		client.HDel(BehaviorStateKey, good, bad, badPair)
		client.SRem(BehaviorDirtyKey, good, bad, badPair)
		client.ZRem(BehaviorProcessingKey, good, bad, badPair)
		client.HDel(BehaviorClaimsKey, good, bad, badPair)
	})
	if err := client.HSet(BehaviorStateKey, good, validState).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(BehaviorStateKey, bad, "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(BehaviorStateKey, badPair, validState).Err(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(client)
	claims := []BehaviorClaim{{Pair: good, ClaimID: "good-claim"}, {Pair: bad, ClaimID: "bad-claim"}, {Pair: badPair, ClaimID: "bad-pair-claim"}}
	deliveries, invalid, err := store.LoadBehaviorDeliveriesWithIssues(t.Context(), claims)
	if err == nil || len(deliveries) != 1 || deliveries[0].Claim.Pair != good || len(invalid) != 2 || invalid[0].Pair != bad || invalid[1].Pair != badPair {
		t.Fatalf("deliveries=%v invalid=%v err=%v", deliveries, invalid, err)
	}
	if state, err := client.HGet(BehaviorStateKey, bad).Result(); err != nil || state != "corrupt" {
		t.Fatalf("bad Behavior State changed: %q err=%v", state, err)
	}
}
