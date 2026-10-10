package likes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestInitializeUserEmptySentinelAndFailClosedIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	userID := postID + 101
	badSetUserID := userID + 1
	wrongTypeUserID := userID + 2
	t.Cleanup(func() {
		client.Del(UserLikesKey(userID), UserLikesKey(badSetUserID), UserLikesKey(wrongTypeUserID), UserLikesOrderKey(userID), UserLikesOrderKey(badSetUserID), UserLikesOrderKey(wrongTypeUserID))
		client.HDel(UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10), strconv.FormatUint(uint64(badSetUserID), 10), strconv.FormatUint(uint64(wrongTypeUserID), 10))
	})

	if err := store.InitializeUserEmpty(context.Background(), 0); err == nil {
		t.Fatal("zero User ID was accepted")
	}
	created, err := store.InitializeUserEmptyWithResult(context.Background(), userID)
	if err != nil || !created {
		t.Fatalf("first initialized-empty result=%t err=%v", created, err)
	}
	if initialized, err := client.SIsMember(UserLikesKey(userID), UserLikesInitSentinel).Result(); err != nil || !initialized {
		t.Fatalf("initialized sentinel=%t err=%v", initialized, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatalf("new empty User Set TTL/Ledger mismatch: %v", err)
	}
	created, err = store.InitializeUserEmptyWithResult(context.Background(), userID)
	if err != nil || created {
		t.Fatalf("idempotent initialization result=%t err=%v", created, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatalf("idempotent initialization TTL/Ledger mismatch: %v", err)
	}
	if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(UserLikesOrderKey(userID), &redis.Z{Score: float64(time.Now().UnixMicro()), Member: strconv.FormatUint(uint64(postID), 10)}).Err(); err != nil {
		t.Fatal(err)
	}
	created, err = store.InitializeUserEmptyWithResult(context.Background(), userID)
	if err != nil || created {
		t.Fatalf("valid existing set was not preserved created=%t err=%v", created, err)
	}
	if liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("idempotent init lost relation=%t err=%v", liked, err)
	}

	if err := client.SAdd(UserLikesKey(badSetUserID), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeUserEmpty(context.Background(), badSetUserID); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("nonempty set without sentinel error=%v want User NotReady compatible with ErrNotReady", err)
	}
	if liked, err := client.SIsMember(UserLikesKey(badSetUserID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("rejected initialization changed existing data liked=%t err=%v", liked, err)
	}
	if err := client.Set(UserLikesKey(wrongTypeUserID), "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeUserEmpty(context.Background(), wrongTypeUserID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("wrong User key type error=%v want ErrLikeRedisType", err)
	}
	if value, err := client.Get(UserLikesKey(wrongTypeUserID)).Result(); err != nil || value != "wrong type" {
		t.Fatalf("wrong type key changed value=%q err=%v", value, err)
	}
}

func TestScanUserLikesReturnsTypedUserReadinessAndTypeErrorsIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	userID := postID + 151
	noSentinelUserID := userID + 1
	wrongTypeUserID := userID + 2
	t.Cleanup(func() {
		client.Del(UserLikesKey(userID), UserLikesKey(noSentinelUserID), UserLikesKey(wrongTypeUserID), UserLikesOrderKey(userID), UserLikesOrderKey(noSentinelUserID), UserLikesOrderKey(wrongTypeUserID))
	})
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(postID+1), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(UserLikesOrderKey(userID), &redis.Z{Score: float64(time.Now().UnixMicro()), Member: strconv.FormatUint(uint64(postID+1), 10)}).Err(); err != nil {
		t.Fatal(err)
	}
	var scanned []uint
	var cursor uint64
	for {
		page, next, err := store.ScanUserLikes(t.Context(), userID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		scanned = append(scanned, page...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(scanned) != 1 || scanned[0] != postID+1 {
		t.Fatalf("scanned=%v want only PostID %d and no sentinel", scanned, postID+1)
	}
	if err := client.SAdd(UserLikesKey(noSentinelUserID), postID+2).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ScanUserLikes(t.Context(), noSentinelUserID, 0, 128); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing sentinel error=%v want User NotReady", err)
	}
	if err := client.LPush(UserLikesKey(wrongTypeUserID), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ScanUserLikes(t.Context(), wrongTypeUserID, 0, 128); !errors.Is(err, ErrUserLikeRedisType) || !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("wrong User key type error=%v want typed Redis key error", err)
	}
}

func TestMutationRequiresInitializedUserAndUnderflowFailsBeforeWritesIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	userIDs := []uint{postID + 101, postID + 102, postID + 103}
	t.Cleanup(func() {
		for _, userID := range userIDs {
			client.Del(UserLikesKey(userID), UserLikesOrderKey(userID))
		}
	})

	if _, err := store.Mutate(ctx, userIDs[0], postID, true); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing User mutation error=%v want User NotReady compatible with ErrNotReady", err)
	}
	if _, err := store.Get(ctx, userIDs[0], postID); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing User Get error=%v want User NotReady compatible with ErrNotReady", err)
	}
	if _, _, err := store.GetMany(ctx, userIDs[0], []uint{postID}); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing User GetMany error=%v want User NotReady compatible with ErrNotReady", err)
	}
	if dirty, err := client.SIsMember(DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("missing User wrote Dirty=%t err=%v", dirty, err)
	}

	if err := client.Set(UserLikesKey(userIDs[0]), "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, userIDs[0], postID, true); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("wrong User type mutation error=%v want ErrLikeRedisType", err)
	}
	if count, err := client.Get(CountKey(postID)).Result(); err != nil || count != "0" {
		t.Fatalf("wrong User type changed count=%q err=%v", count, err)
	}
	if version, err := client.Get(VersionKey(postID)).Result(); err != nil || version != "0" {
		t.Fatalf("wrong User type changed version=%q err=%v", version, err)
	}

	if err := client.Del(UserLikesKey(userIDs[0]), UserLikesOrderKey(userIDs[0])).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(UserLikesKey(userIDs[1]), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, userIDs[1], postID, false); !errors.Is(err, ErrUserLikeNotReady) || !errors.Is(err, ErrNotReady) {
		t.Fatalf("missing sentinel mutation error=%v want User NotReady compatible with ErrNotReady", err)
	}
	if liked, err := client.SIsMember(UserLikesKey(userIDs[1]), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("missing sentinel mutation changed relation=%t err=%v", liked, err)
	}

	if err := store.InitializeUserEmpty(ctx, userIDs[2]); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(UserLikesKey(userIDs[2]), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(UserLikesOrderKey(userIDs[2]), &redis.Z{Score: float64(time.Now().UnixMicro()), Member: strconv.FormatUint(uint64(postID), 10)}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, userIDs[2], postID, false); !errors.Is(err, ErrLikeCountInconsistent) {
		t.Fatalf("underflow mutation error=%v want ErrLikeCountInconsistent", err)
	}
	if liked, err := client.SIsMember(UserLikesKey(userIDs[2]), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !liked {
		t.Fatalf("underflow changed relation=%t err=%v", liked, err)
	}
	if dirty, err := client.SIsMember(DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("underflow wrote Dirty=%t err=%v", dirty, err)
	}
}

func TestConcurrentUserInitializationAndMutationPreserveRelationIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	userID := postID + 101
	t.Cleanup(func() { client.Del(UserLikesKey(userID), UserLikesOrderKey(userID)) })
	start := make(chan struct{})
	var wait sync.WaitGroup
	var initErr, mutateErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		initErr = store.InitializeUserEmpty(ctx, userID)
	}()
	go func() {
		defer wait.Done()
		<-start
		_, mutateErr = store.Mutate(ctx, userID, postID, true)
	}()
	close(start)
	wait.Wait()
	if initErr != nil {
		t.Fatal(initErr)
	}
	if mutateErr != nil && !errors.Is(mutateErr, ErrNotReady) {
		t.Fatalf("concurrent mutation error=%v", mutateErr)
	}
	if mutateErr != nil {
		if _, err := store.Mutate(ctx, userID, postID, true); err != nil {
			t.Fatal(err)
		}
	}
	state, err := store.Get(ctx, userID, postID)
	if err != nil || !state.Liked || state.Count != 1 || state.Version != 1 {
		t.Fatalf("concurrent init/mutation state=%+v err=%v", state, err)
	}
	if err := assertUserLikeTTLAndLedgerMatch(t, client, userID); err != nil {
		t.Fatalf("concurrent User Set TTL/Ledger mismatch: %v", err)
	}
}

func TestConcurrentSameAndDifferentUsersCountChangesOnceIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	t.Cleanup(func() { client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userIDs := make([]uint, 33)
	for index := range userIDs {
		userIDs[index] = postID + uint(index) + 100
	}
	t.Cleanup(func() {
		client.Del(ReadyKey(postID), CountKey(postID), VersionKey(postID))
		for _, userID := range userIDs {
			client.Del(UserLikesKey(userID), UserLikesOrderKey(userID))
			cleanupRecoverableStoreBehaviorPair(client, userID, postID)
		}
		client.SRem(DirtyKey, postID)
		client.SRem(RegistryKey, postID)
		client.ZRem(ExpiryCandidatesKey, postID)
		client.HDel(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10))
	})
	if created, err := initializeLikeStore(store, context.Background(), postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	for _, userID := range userIDs {
		if err := store.InitializeUserEmpty(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
	}

	const sameUserCalls = 24
	var sameWait sync.WaitGroup
	var sameErr error
	var errMu sync.Mutex
	sameWait.Add(sameUserCalls)
	for range sameUserCalls {
		go func() {
			defer sameWait.Done()
			if _, err := store.Mutate(context.Background(), userIDs[0], postID, true); err != nil {
				errMu.Lock()
				sameErr = err
				errMu.Unlock()
			}
		}()
	}
	sameWait.Wait()
	if sameErr != nil {
		t.Fatal(sameErr)
	}

	var manyWait sync.WaitGroup
	var manyErr error
	manyWait.Add(len(userIDs) - 1)
	for _, userID := range userIDs[1:] {
		userID := userID
		go func() {
			defer manyWait.Done()
			if _, err := store.Mutate(context.Background(), userID, postID, true); err != nil {
				errMu.Lock()
				manyErr = err
				errMu.Unlock()
			}
		}()
	}
	manyWait.Wait()
	if manyErr != nil {
		t.Fatal(manyErr)
	}
	states, unavailable, err := store.GetMany(context.Background(), userIDs[0], []uint{postID})
	if err != nil || len(unavailable) != 0 || states[postID].Count != int64(len(userIDs)) || !states[postID].Liked || states[postID].Version != int64(len(userIDs)) {
		t.Fatalf("concurrent state=%v unavailable=%v err=%v", states, unavailable, err)
	}
	for _, userID := range userIDs[1:] {
		liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || !liked {
			t.Fatalf("user=%d relation=%t err=%v", userID, liked, err)
		}
	}
}

func TestMutationInvalidCountAndVersionFailClosedIntegration(t *testing.T) {
	client, store, postID := openRecoverableStoreIntegration(t)
	ctx := context.Background()
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	userID := postID + 101
	t.Cleanup(func() { client.Del(UserLikesKey(userID), UserLikesOrderKey(userID)) })
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []struct {
		key   string
		value string
	}{
		{key: CountKey(postID), value: "not-an-integer"},
		{key: VersionKey(postID), value: "not-an-integer"},
	} {
		if err := client.Set(malformed.key, malformed.value, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Mutate(ctx, userID, postID, true); !errors.Is(err, ErrNotReady) {
			t.Fatalf("key=%s error=%v want ErrNotReady", malformed.key, err)
		}
		if liked, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || liked {
			t.Fatalf("key=%s malformed state changed User relation=%t err=%v", malformed.key, liked, err)
		}
		if err := client.Set(malformed.key, "0", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUserLikesKeyNamingContract(t *testing.T) {
	if got := UserLikesKey(101); got != fmt.Sprintf("user:likes:%d", 101) {
		t.Fatalf("UserLikesKey=%q", got)
	}
}
