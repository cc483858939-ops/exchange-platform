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
	ctx := context.Background()
	cleanup := func() {
		client.Del(ReadyKey(articleID), CountKey(articleID), UsersKey(articleID), VersionKey(articleID))
		client.SRem(DirtyKey, articleID)
		client.ZRem(ProcessingKey, articleID)
		client.HDel(ClaimsKey, strconv.FormatUint(uint64(articleID), 10))
	}
	cleanup()
	defer cleanup()
	created, err := store.Initialize(ctx, articleID, 0, 0, nil)
	if err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	first, err := store.Mutate(ctx, 11, articleID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Liked || first.Count != 1 || first.Version != 1 {
		t.Fatalf("first mutation=%+v", first)
	}
	duplicate, err := store.Mutate(ctx, 11, articleID, true)
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
	last, err := store.Mutate(ctx, 11, articleID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !last.Changed || last.Liked || last.Count != 0 || last.Version != 2 {
		t.Fatalf("unlike=%+v", last)
	}
}

func findClaim(claims []SnapshotClaim, articleID uint) (SnapshotClaim, bool) {
	for _, claim := range claims {
		if claim.ArticleID == articleID {
			return claim, true
		}
	}
	return SnapshotClaim{}, false
}
