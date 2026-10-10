package tasks

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func TestUserLikeRelationCleanupUsesSQLLifecycleAndPreservesSentinelIntegration(t *testing.T) {
	dsn, addr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("user_like_cleanup_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz)",
		"CREATE TABLE posts (id bigint PRIMARY KEY, deleted_at timestamptz)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	t.Cleanup(func() { _ = client.Close() })
	store := likes.NewStore(client)
	baseID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID, deletedID, activeID, missingReadyActiveID, missingSQLID := baseID+101, baseID+1, baseID+2, baseID+3, baseID+4
	t.Cleanup(func() {
		_ = client.Del(likes.UserLikesKey(userID), likes.UserLikesOrderKey(userID), likes.ReadyKey(deletedID), likes.CountKey(deletedID), likes.VersionKey(deletedID), likes.ReadyKey(activeID), likes.CountKey(activeID), likes.VersionKey(activeID), likes.ReadyKey(missingSQLID), likes.CountKey(missingSQLID), likes.VersionKey(missingSQLID)).Err()
	})
	if err := tx.Exec("INSERT INTO users (id) VALUES (?)", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO posts (id) VALUES (?), (?)", activeID, missingReadyActiveID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO posts (id, deleted_at) VALUES (?, now())", deletedID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.ReadyKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.CountKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.VersionKey(activeID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), deletedID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), missingSQLID); err != nil {
		t.Fatal(err)
	}
	if err := addUserLikeCleanupRelations(client, userID, deletedID, activeID, missingReadyActiveID, missingSQLID); err != nil {
		t.Fatal(err)
	}
	state := userLikeRelationCleanupState{}
	for i := 0; i < 4; i++ {
		if err := runUserLikeRelationCleanupPass(t.Context(), store, tx, &state); err != nil {
			t.Fatal(err)
		}
	}
	for member, want := range map[uint]bool{
		0: true, deletedID: false, activeID: true, missingReadyActiveID: true, missingSQLID: false,
	} {
		has, err := client.SIsMember(likes.UserLikesKey(userID), strconv.FormatUint(uint64(member), 10)).Result()
		if err != nil || has != want {
			t.Fatalf("User relation post=%d present=%t err=%v want=%t", member, has, err, want)
		}
	}
	if removed, err := store.RemoveDeletedUserPostRelations(t.Context(), userID, make([]uint, 129)); err == nil || removed != 0 {
		t.Fatalf("oversized cleanup batch removed=%d err=%v", removed, err)
	}
}

type userLikeCleanupFixture struct {
	db     *gorm.DB
	client *redis.Client
	store  *likes.Store
	baseID uint
}

func newUserLikeCleanupFixture(t *testing.T) *userLikeCleanupFixture {
	t.Helper()
	dsn, addr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	schema := fmt.Sprintf("user_like_cleanup_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz)",
		"CREATE TABLE posts (id bigint PRIMARY KEY, deleted_at timestamptz)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	fixture := &userLikeCleanupFixture{
		db: tx, client: client, store: likes.NewStore(client),
		baseID: uint(time.Now().UnixNano() & 0x3fffffff),
	}
	t.Cleanup(func() {
		_ = client.Del(fixture.keys()...).Err()
		_ = client.Close()
		_ = tx.Rollback().Error
	})
	return fixture
}

func (f *userLikeCleanupFixture) keys() []string {
	keys := make([]string, 0, 5)
	for offset := uint(1); offset <= 256; offset++ {
		keys = append(keys, likes.UserLikesKey(f.baseID+offset), likes.UserLikesOrderKey(f.baseID+offset), likes.ReadyKey(f.baseID+offset), likes.CountKey(f.baseID+offset), likes.VersionKey(f.baseID+offset))
	}
	return keys
}

func (f *userLikeCleanupFixture) addUser(t *testing.T, id uint) {
	t.Helper()
	if err := f.db.Exec("INSERT INTO users (id) VALUES (?)", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.InitializeUserEmpty(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func (f *userLikeCleanupFixture) runPasses(t *testing.T, state *userLikeRelationCleanupState, count int) {
	t.Helper()
	for range count {
		if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, state); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUserLikeRelationCleanupStartsAnotherRoundAfterEmptyPageIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	userA, userB, deletedPost := f.baseID+1, f.baseID+2, f.baseID+3
	f.addUser(t, userA)
	f.addUser(t, userB)
	state := userLikeRelationCleanupState{}
	f.runPasses(t, &state, 3) // A, B, then the empty SQL page resets the cycle.
	if state.lastUserID != 0 || state.userID != 0 {
		t.Fatalf("cycle state after empty page=%+v, want reset", state)
	}
	if err := addUserLikeCleanupRelations(f.client, userA, deletedPost); err != nil {
		t.Fatal(err)
	}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if liked, err := f.client.SIsMember(likes.UserLikesKey(userA), strconv.FormatUint(uint64(deletedPost), 10)).Result(); err != nil || liked {
		t.Fatalf("second round relation present=%t err=%v", liked, err)
	}
}

func TestUserLikeRelationCleanupEmptyDatabaseCanSeeLaterUsersIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	state := userLikeRelationCleanupState{lastUserID: f.baseID + 10}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if state.lastUserID != 0 || state.userID != 0 || len(state.users) != 0 {
		t.Fatalf("empty-table cycle state=%+v, want reset", state)
	}
	userID, deletedPost := f.baseID+1, f.baseID+2
	f.addUser(t, userID)
	if err := addUserLikeCleanupRelations(f.client, userID, deletedPost); err != nil {
		t.Fatal(err)
	}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if liked, err := f.client.SIsMember(likes.UserLikesKey(userID), strconv.FormatUint(uint64(deletedPost), 10)).Result(); err != nil || liked {
		t.Fatalf("new User relation present=%t err=%v", liked, err)
	}
}

func TestUserLikeRelationCleanupPagesMoreThanOneUserPageIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	const users = userLikeCleanupUserPageSize + 1
	postID := f.baseID + 200
	state := userLikeRelationCleanupState{}
	for index := 1; index <= users; index++ {
		userID := f.baseID + uint(index)
		f.addUser(t, userID)
		if err := addUserLikeCleanupRelations(f.client, userID, postID); err != nil {
			t.Fatal(err)
		}
	}
	f.runPasses(t, &state, users+1) // Includes the empty page that closes the round.
	if state.lastUserID != 0 {
		t.Fatalf("lastUserID=%d after full round, want 0", state.lastUserID)
	}
	for index := 1; index <= users; index++ {
		userID := f.baseID + uint(index)
		liked, err := f.client.SIsMember(likes.UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || liked {
			t.Fatalf("User %d relation present=%t err=%v", userID, liked, err)
		}
	}
}

func TestUserLikeRelationCleanupBoundsOverReturnedScanMembersIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	userA, userB := f.baseID+1, f.baseID+2
	f.addUser(t, userA)
	f.addUser(t, userB)
	candidates := make([]uint, 300)
	for index := range candidates {
		candidates[index] = f.baseID + 1000 + uint(index)
	}
	type scanCall struct {
		userID uint
		cursor uint64
		count  int
	}
	var scans []scanCall
	var removalSizes []int
	state := userLikeRelationCleanupState{
		scanUserLikes: func(_ context.Context, _ *likes.Store, userID uint, cursor uint64, count int) ([]uint, uint64, error) {
			scans = append(scans, scanCall{userID: userID, cursor: cursor, count: count})
			if userID == userA && cursor == 0 {
				return append([]uint(nil), candidates...), 41, nil // COUNT is only a hint.
			}
			return nil, 0, nil
		},
		removeRelations: func(_ context.Context, _ *likes.Store, userID uint, postIDs []uint) (int64, []likes.UserLikeCleanupIssue, error) {
			if userID != userA {
				t.Fatalf("unexpected UserID=%d removal", userID)
			}
			removalSizes = append(removalSizes, len(postIDs))
			return int64(len(postIDs)), nil, nil
		},
	}
	store := likes.NewStore(nil)
	for range 6 {
		if err := runUserLikeRelationCleanupPass(t.Context(), store, f.db, &state); err != nil {
			t.Fatal(err)
		}
	}
	if len(removalSizes) != 3 || removalSizes[0] != 128 || removalSizes[1] != 128 || removalSizes[2] != 44 {
		t.Fatalf("removal batch sizes=%v want [128 128 44]", removalSizes)
	}
	if len(scans) != 3 || scans[0] != (scanCall{userID: userA, cursor: 0, count: 128}) ||
		scans[1] != (scanCall{userID: userA, cursor: 41, count: 128}) ||
		scans[2] != (scanCall{userID: userB, cursor: 0, count: 128}) {
		t.Fatalf("scan calls=%+v want User A cursor 0/41 then User B", scans)
	}
	if state.lastUserID != 0 || state.userID != 0 {
		t.Fatalf("state after full round=%+v, want cycle reset", state)
	}
}

func TestUserLikeRelationCleanupSkipsCorruptUserAndRetriesInfrastructureFailureIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	userA, userB, missingPost := f.baseID+1, f.baseID+2, f.baseID+3
	if err := f.db.Exec("INSERT INTO users (id) VALUES (?), (?)", userA, userB).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.client.LPush(likes.UserLikesKey(userA), "corrupt-list-member").Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.InitializeUserEmpty(t.Context(), userB); err != nil {
		t.Fatal(err)
	}
	if err := addUserLikeCleanupRelations(f.client, userB, missingPost); err != nil {
		t.Fatal(err)
	}
	state := userLikeRelationCleanupState{}
	badRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 25 * time.Millisecond, ReadTimeout: 25 * time.Millisecond, WriteTimeout: 25 * time.Millisecond})
	defer badRedis.Close()
	if err := runUserLikeRelationCleanupPass(t.Context(), likes.NewStore(badRedis), f.db, &state); err == nil {
		t.Fatal("unavailable Redis must be returned for retry")
	}
	if state.userID != userA {
		t.Fatalf("transient Redis failure skipped current User: state=%+v", state)
	}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if state.userID != userB {
		t.Fatalf("corrupt User did not advance to next User: state=%+v", state)
	}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if kind, err := f.client.Type(likes.UserLikesKey(userA)).Result(); err != nil || kind != "list" {
		t.Fatalf("corrupt User state changed type=%q err=%v", kind, err)
	}
	if liked, err := f.client.SIsMember(likes.UserLikesKey(userB), strconv.FormatUint(uint64(missingPost), 10)).Result(); err != nil || liked {
		t.Fatalf("healthy User relation present=%t err=%v", liked, err)
	}
	if err := f.client.Del(likes.UserLikesKey(userA)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.InitializeUserEmpty(t.Context(), userA); err != nil {
		t.Fatal(err)
	}
	if err := addUserLikeCleanupRelations(f.client, userA, missingPost); err != nil {
		t.Fatal(err)
	}
	f.runPasses(t, &state, 3) // User A is retried on the next full round.
	if liked, err := f.client.SIsMember(likes.UserLikesKey(userA), strconv.FormatUint(uint64(missingPost), 10)).Result(); err != nil || liked {
		t.Fatalf("repaired User relation present=%t err=%v", liked, err)
	}
}

func TestUserLikeRelationCleanupSkipsLegalColdSetAndRetainsLedgerIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	userID := f.baseID + 1
	f.addUser(t, userID)
	uid := strconv.FormatUint(uint64(userID), 10)
	t.Cleanup(func() { _ = f.client.HDel(likes.UserLikesExpiryLedgerKey, uid).Err() })
	if err := f.client.Eval(`
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local expiry = now_ms + tonumber(ARGV[2])
redis.call('PEXPIREAT', KEYS[1], expiry)
redis.call('HSET', KEYS[2], ARGV[1], tostring(expiry))
return expiry
`, []string{likes.UserLikesKey(userID), likes.UserLikesExpiryLedgerKey}, uid, 100).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		exists, err := f.client.Exists(likes.UserLikesKey(userID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test User Like Set did not expire")
		}
		time.Sleep(5 * time.Millisecond)
	}

	state := userLikeRelationCleanupState{}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if state.userID != 0 {
		t.Fatalf("cold User was not skipped by cleanup: state=%+v", state)
	}
	if exists, err := f.client.Exists(likes.UserLikesKey(userID)).Result(); err != nil || exists != 0 {
		t.Fatalf("cleanup recreated cold User Set exists=%d err=%v", exists, err)
	}
	if ledger, err := f.client.HGet(likes.UserLikesExpiryLedgerKey, uid).Result(); err != nil || ledger == "" {
		t.Fatalf("cleanup removed cold User ledger=%q err=%v", ledger, err)
	}
}

func TestUserLikeRelationCleanupProtectsPostStateIssuesAndHandlesExpiredTombstonesIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	userID := f.baseID + 1
	f.addUser(t, userID)
	activeMissingReady := f.baseID + 2
	deletedReadyActive := f.baseID + 3
	deletedReadyWrongType := f.baseID + 4
	deletedTombstone := f.baseID + 5
	deletedExpiredTombstone := f.baseID + 6
	if err := f.db.Exec("INSERT INTO posts (id) VALUES (?)", activeMissingReady).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO posts (id, deleted_at) VALUES (?, now()), ( ?, now()), (?, now()), (?, now())", deletedReadyActive, deletedReadyWrongType, deletedTombstone, deletedExpiredTombstone).Error; err != nil {
		t.Fatal(err)
	}
	for _, postID := range []uint{deletedReadyActive, deletedTombstone, deletedExpiredTombstone} {
		if err := f.client.Set(likes.CountKey(postID), "7", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := f.client.Set(likes.VersionKey(postID), "9", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.client.Set(likes.ReadyKey(deletedReadyActive), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.RPush(likes.ReadyKey(deletedReadyWrongType), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Set(likes.ReadyKey(deletedTombstone), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Set(likes.ReadyKey(deletedExpiredTombstone), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.PExpire(likes.ReadyKey(deletedExpiredTombstone), time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		exists, err := f.client.Exists(likes.ReadyKey(deletedExpiredTombstone)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tombstone did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	postIDs := []uint{activeMissingReady, deletedReadyActive, deletedReadyWrongType, deletedTombstone, deletedExpiredTombstone}
	for _, postID := range postIDs {
		if err := addUserLikeCleanupRelations(f.client, userID, postID); err != nil {
			t.Fatal(err)
		}
	}
	pair := likes.BehaviorPair(userID, deletedTombstone)
	t.Cleanup(func() {
		_ = f.client.SRem(likes.DirtyKey, deletedTombstone).Err()
		_ = f.client.SRem(likes.BehaviorDirtyKey, pair).Err()
	})
	if err := f.client.SAdd(likes.DirtyKey, deletedTombstone).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.SAdd(likes.BehaviorDirtyKey, pair).Err(); err != nil {
		t.Fatal(err)
	}
	state := userLikeRelationCleanupState{}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	for postID, wantPresent := range map[uint]bool{
		activeMissingReady: true, deletedReadyActive: true, deletedReadyWrongType: true,
		deletedTombstone: false, deletedExpiredTombstone: false,
	} {
		present, err := f.client.SIsMember(likes.UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || present != wantPresent {
			t.Fatalf("Post %d relation present=%t err=%v want=%t", postID, present, err, wantPresent)
		}
	}
	for _, postID := range []uint{deletedReadyActive, deletedTombstone, deletedExpiredTombstone} {
		count, countErr := f.client.Get(likes.CountKey(postID)).Result()
		version, versionErr := f.client.Get(likes.VersionKey(postID)).Result()
		if countErr != nil || versionErr != nil || count != "7" || version != "9" {
			t.Fatalf("cleanup changed aggregate Post=%d count=%q/%v version=%q/%v", postID, count, countErr, version, versionErr)
		}
	}
	if dirty, err := f.client.SIsMember(likes.DirtyKey, strconv.FormatUint(uint64(deletedTombstone), 10)).Result(); err != nil || !dirty {
		t.Fatalf("snapshot dirty changed dirty=%t err=%v", dirty, err)
	}
	if dirty, err := f.client.SIsMember(likes.BehaviorDirtyKey, pair).Result(); err != nil || !dirty {
		t.Fatalf("behavior dirty changed dirty=%t err=%v", dirty, err)
	}
	if sentinel, err := f.client.SIsMember(likes.UserLikesKey(userID), likes.UserLikesInitSentinel).Result(); err != nil || !sentinel {
		t.Fatalf("User sentinel present=%t err=%v", sentinel, err)
	}
	if err := runUserLikeRelationCleanupPass(t.Context(), f.store, f.db, &state); err != nil {
		t.Fatal(err)
	}
	if sentinel, err := f.client.SIsMember(likes.UserLikesKey(userID), likes.UserLikesInitSentinel).Result(); err != nil || !sentinel {
		t.Fatalf("repeat cleanup removed sentinel=%t err=%v", sentinel, err)
	}
}

func TestUserLikeRelationCleanupSQLFailureDoesNotResetCycleIntegration(t *testing.T) {
	f := newUserLikeCleanupFixture(t)
	state := userLikeRelationCleanupState{lastUserID: f.baseID + 50}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runUserLikeRelationCleanupPass(ctx, f.store, f.db, &state)
	if err == nil {
		t.Fatal("canceled SQL query must be returned, not treated as an empty page")
	}
	if state.lastUserID != f.baseID+50 {
		t.Fatalf("SQL failure reset UserID cursor to %d", state.lastUserID)
	}
}

func addUserLikeCleanupRelations(client *redis.Client, userID uint, postIDs ...uint) error {
	if len(postIDs) == 0 {
		return nil
	}
	setMembers := make([]interface{}, 0, len(postIDs))
	orderedMembers := make([]*redis.Z, 0, len(postIDs))
	baseScore := time.Now().UnixMicro()
	for index, postID := range postIDs {
		member := strconv.FormatUint(uint64(postID), 10)
		setMembers = append(setMembers, member)
		orderedMembers = append(orderedMembers, &redis.Z{Score: float64(baseScore + int64(index)), Member: member})
	}
	if err := client.SAdd(likes.UserLikesKey(userID), setMembers...).Err(); err != nil {
		return err
	}
	return client.ZAdd(likes.UserLikesOrderKey(userID), orderedMembers...).Err()
}
