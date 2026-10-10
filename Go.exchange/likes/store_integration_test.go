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
		client.SRem(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10))
		client.ZRem(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postID), 10))
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
		t.Fatal(err)
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
	if initialized, err := client.SIsMember(UserLikesKey(userID), UserLikesInitSentinel).Result(); err != nil || !initialized {
		t.Fatalf("last unlike removed User sentinel initialized=%t err=%v", initialized, err)
	}
	if liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || liked {
		t.Fatalf("last unlike retained relation liked=%t err=%v", liked, err)
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
	postIDs := []uint{base, base + 1, base + 2, base + 3}
	ctx := context.Background()
	cleanup := func() {
		for _, postID := range postIDs {
			client.Del(ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID))
			client.SRem(UserLikesKey(11), strconv.FormatUint(uint64(postID), 10))
			client.ZRem(UserLikesOrderKey(11), strconv.FormatUint(uint64(postID), 10))
			client.SRem(DirtyKey, postID)
			client.ZRem(ProcessingKey, postID)
			client.HDel(ClaimsKey, strconv.FormatUint(uint64(postID), 10))
			client.SRem(RegistryKey, postID)
			client.ZRem(ExpiryCandidatesKey, postID)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10))
			pair := BehaviorPair(11, postID)
			client.SRem(BehaviorDirtyKey, pair)
			client.HDel(BehaviorStateKey, pair)
			client.ZRem(BehaviorProcessingKey, pair)
			client.HDel(BehaviorClaimsKey, pair)
		}
	}
	cleanup()
	defer cleanup()

	if created, err := initializeLikeStore(store, ctx, postIDs[0], 1, 1, []uint{11}); err != nil || !created {
		t.Fatalf("article A initialize created=%t err=%v", created, err)
	}
	if created, err := initializeLikeStore(store, ctx, postIDs[1], 0, 0, nil); err != nil || !created {
		t.Fatalf("article B initialize created=%t err=%v", created, err)
	}
	if created, err := initializeLikeStore(store, ctx, postIDs[3], 0, 0, nil); err != nil || !created {
		t.Fatalf("article D initialize created=%t err=%v", created, err)
	}
	if mutation, err := store.Mutate(ctx, 11, postIDs[3], true); err != nil || !mutation.Changed {
		t.Fatalf("article D mutation=%+v err=%v", mutation, err)
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
	if states[postIDs[3]].Count != 1 || !states[postIDs[3]].Liked {
		t.Fatalf("article D state=%+v", states[postIDs[3]])
	}
	if !equalUintSlices(unavailable, []uint{postIDs[2]}) {
		t.Fatalf("unavailable=%v", unavailable)
	}
	if mutation, err := store.Mutate(ctx, 11, postIDs[3], false); err != nil || !mutation.Changed || mutation.Count != 0 {
		t.Fatalf("article D unlike=%+v err=%v", mutation, err)
	}
	if state, err := store.Get(ctx, 11, postIDs[0]); err != nil || !state.Liked || state.Count != 1 {
		t.Fatalf("article A changed with article D state=%+v err=%v", state, err)
	}
}

func TestStoreGetManyDeletedCountWrongTypeIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}

	store := NewStore(client)
	base := uint(time.Now().UnixNano() & 0x3fffffff)
	deletedID, liveID, userID := base, base+1, base+2
	t.Cleanup(func() {
		for _, id := range []uint{deletedID, liveID} {
			client.Del(ReadyKey(id), CountKey(id), UsersKey(id), VersionKey(id))
			client.SRem(RegistryKey, id)
			client.ZRem(ExpiryCandidatesKey, id)
			client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(id), 10))
		}
		client.Del(UserLikesKey(userID), UserLikesOrderKey(userID))
	})
	ctx := t.Context()
	if created, err := initializeLikeStore(store, ctx, liveID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize live Post created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ReadyKey(deletedID), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(CountKey(deletedID), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}

	for name, read := range map[string]func() (map[uint]State, []uint, error){
		"GetMany": func() (map[uint]State, []uint, error) { return store.GetMany(ctx, userID, []uint{deletedID, liveID}) },
		"GetManyForServing": func() (map[uint]State, []uint, error) {
			return store.GetManyForServing(ctx, userID, []uint{deletedID, liveID}, 0, 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			states, unavailable, err := read()
			if err != nil || len(states) != 1 || states[liveID] != (State{}) || !equalUintSlices(unavailable, []uint{deletedID}) {
				t.Fatalf("states=%v unavailable=%v err=%v", states, unavailable, err)
			}
		})
	}
	if err := client.Del(CountKey(liveID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(CountKey(liveID), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetMany(ctx, userID, []uint{deletedID, liveID}); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("active Post Count type error=%v want ErrLikeRedisType", err)
	}
}

func TestStoreGetManyClosedClientFailsClosed(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	states, unavailable, err := NewStore(client).GetMany(t.Context(), 0, []uint{1})
	if err == nil || states != nil || unavailable != nil {
		t.Fatalf("closed Redis client: states=%v unavailable=%v err=%v", states, unavailable, err)
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
			client.SRem(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10))
			client.ZRem(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postID), 10))
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
		if created, err := initializeLikeStore(store, ctx, postID, 1, 7, []uint{userID}); err != nil || !created {
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
	for _, postID := range []uint{target, unrelated} {
		if liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
			t.Fatalf("PurgePost changed User relation post=%d liked=%t err=%v", postID, liked, err)
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

	for _, key := range []string{ReadyKey(unrelated), CountKey(unrelated), VersionKey(unrelated)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("unrelated key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if exists, err := client.Exists(UsersKey(unrelated)).Result(); err != nil || exists != 0 {
		t.Fatalf("legacy unrelated Post Users key=%q exists=%d err=%v", UsersKey(unrelated), exists, err)
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
