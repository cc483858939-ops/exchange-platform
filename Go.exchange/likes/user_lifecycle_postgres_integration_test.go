package likes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/internal/testdb"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func TestUserLikeColdRestoreUsesPostReactionSnapshotIntegration(t *testing.T) {
	dsn, redisAddr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR to run User Like PostgreSQL/Redis integration test")
	}
	db := openUserLikeLifecyclePostgresIntegration(t, dsn)
	settings := userLikeIntegrationSettings(true, 300*time.Millisecond)
	client, store, userID, p1 := openUserLikeLifecycleRedisIntegration(t, settings)
	p2, p3, p4, p5, pMissing := p1+1, p1+2, p1+3, p1+4, p1+5
	postIDs := []uint{p1, p2, p3, p4, p5}
	t.Cleanup(func() {
		for _, postID := range postIDs {
			cleanupUserLikeLifecyclePostState(client, postID)
		}
	})

	if err := db.Exec("INSERT INTO users (id, deleted_at) VALUES (?, NULL)", userID).Error; err != nil {
		t.Fatal(err)
	}
	for _, post := range []struct {
		id         uint
		deletedAt  any
		visibility string
	}{
		{id: p1, visibility: "public"},
		{id: p2, visibility: "public"},
		{id: p3, visibility: "public"},
		{id: p4, deletedAt: time.Now(), visibility: "public"},
		{id: p5, visibility: "private"},
	} {
		if err := db.Exec("INSERT INTO posts (id, deleted_at, visibility) VALUES (?, ?, ?)", post.id, post.deletedAt, post.visibility).Error; err != nil {
			t.Fatalf("insert Post %d: %v", post.id, err)
		}
	}
	for _, row := range []struct {
		postID uint
		liked  bool
	}{
		{p1, true}, {p2, true}, {p3, false}, {p4, true}, {p5, true}, {pMissing, true},
	} {
		if err := db.Exec(`INSERT INTO post_reaction (user_id, post_id, reaction, liked, reaction_version, updated_at, state_changed_at)
VALUES (?, ?, 1, ?, 1, now(), now())`, userID, row.postID, row.liked).Error; err != nil {
			t.Fatalf("insert reaction for Post %d: %v", row.postID, err)
		}
	}

	for _, post := range []struct {
		id      uint
		count   string
		version string
	}{{p1, "1", "9"}, {p2, "1", "4"}, {p5, "1", "3"}} {
		if err := client.Set(ReadyKey(post.id), "1", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(CountKey(post.id), post.count, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(VersionKey(post.id), post.version, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	for _, postID := range []uint{p1, p2, p4, p5, pMissing} {
		if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Err(); err != nil {
			t.Fatal(err)
		}
	}
	awaitUserLikeSetExpiry(t, client, userID, 2*time.Second)

	lifecycle := NewUserLikeLifecycle(store, db)
	var wait sync.WaitGroup
	errCh := make(chan error, 4)
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errCh <- lifecycle.RecoverIfCold(context.Background(), userID)
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent cold restore: %v", err)
		}
	}

	for _, postID := range []uint{p1, p2, p5} {
		member, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || !member {
			t.Fatalf("restored Post %d member=%t err=%v", postID, member, err)
		}
	}
	for _, postID := range []uint{p3, p4, pMissing} {
		member, err := client.SIsMember(UserLikesKey(userID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || member {
			t.Fatalf("filtered Post %d member=%t err=%v", postID, member, err)
		}
	}
	if card, err := client.SCard(UserLikesKey(userID)).Result(); err != nil || card != 4 {
		t.Fatalf("restored relation Set cardinality=%d err=%v want sentinel + 3 relations", card, err)
	}
	for _, post := range []struct {
		id      uint
		count   string
		version string
	}{{p1, "1", "9"}, {p2, "1", "4"}, {p5, "1", "3"}} {
		count, err := client.Get(CountKey(post.id)).Result()
		if err != nil || count != post.count {
			t.Fatalf("restore changed Count for Post %d: %q err=%v", post.id, count, err)
		}
		version, err := client.Get(VersionKey(post.id)).Result()
		if err != nil || version != post.version {
			t.Fatalf("restore changed Version for Post %d: %q err=%v", post.id, version, err)
		}
	}

	idempotent, err := store.Mutate(t.Context(), userID, p1, true)
	if err != nil || idempotent.Changed || idempotent.Count != 1 || idempotent.Version != 9 {
		t.Fatalf("Like after restore=%+v err=%v", idempotent, err)
	}
	unlike, err := store.Mutate(t.Context(), userID, p2, false)
	if err != nil || !unlike.Changed || unlike.Count != 0 || unlike.Version != 5 {
		t.Fatalf("Unlike after restore=%+v err=%v", unlike, err)
	}
	if ttl, err := client.PTTL(UserLikesKey(userID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("restored User Set TTL=%s err=%v", ttl, err)
	}
}

func TestUserLikeColdRestoreRejectsRelationLimitWithoutPublishingPartialSetIntegration(t *testing.T) {
	dsn, redisAddr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR to run User Like restore limit integration test")
	}
	db := openUserLikeLifecyclePostgresIntegration(t, dsn)
	settings := userLikeIntegrationSettings(true, 250*time.Millisecond)
	settings.RestoreMaxRelations = 2
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, settings)
	if err := db.Exec("INSERT INTO users (id, deleted_at) VALUES (?, NULL)", userID).Error; err != nil {
		t.Fatal(err)
	}
	for offset := uint(0); offset < 3; offset++ {
		id := postID + offset
		if err := db.Exec("INSERT INTO posts (id, deleted_at, visibility) VALUES (?, NULL, 'private')", id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO post_reaction (user_id, post_id, reaction, liked, reaction_version, updated_at, state_changed_at)
VALUES (?, ?, 1, TRUE, 1, now(), now())`, userID, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	awaitUserLikeSetExpiry(t, client, userID, 2*time.Second)
	err := NewUserLikeLifecycle(store, db).RecoverIfCold(context.Background(), userID)
	if !errors.Is(err, ErrUserLikeRecoveryTooLarge) {
		t.Fatalf("over-limit restore error=%v want ErrUserLikeRecoveryTooLarge", err)
	}
	if exists, err := client.Exists(UserLikesKey(userID)).Result(); err != nil || exists != 0 {
		t.Fatalf("over-limit restore published partial Set exists=%d err=%v", exists, err)
	}
}

func TestUserLikeColdRestoreDoesNotInstallRelationAfterConcurrentPostDeleteIntegration(t *testing.T) {
	dsn, redisAddr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR to run User Like restore/Post delete race integration test")
	}
	db := openUserLikeLifecyclePostgresIntegration(t, dsn)
	var schemaName string
	if err := db.Raw("SELECT current_schema()").Scan(&schemaName).Error; err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^user_like_restore_[0-9]+$`).MatchString(schemaName) {
		t.Fatalf("unexpected restore integration schema %q", schemaName)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(3)
	client, store, userID, postID := openUserLikeLifecycleRedisIntegration(t, userLikeIntegrationSettings(true, 300*time.Millisecond))
	if err := db.Exec("INSERT INTO users (id, deleted_at) VALUES (?, NULL)", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO posts (id, deleted_at, visibility) VALUES (?, NULL, 'private')", postID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO post_reaction (user_id, post_id, reaction, liked, reaction_version, updated_at, state_changed_at)
VALUES (?, ?, 1, TRUE, 1, now(), now())`, userID, postID).Error; err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{ReadyKey(postID): "1", CountKey(postID): "1", VersionKey(postID): "1"} {
		if err := client.Set(key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	awaitUserLikeSetExpiry(t, client, userID, 2*time.Second)

	reactionQueryReached := make(chan struct{})
	continueRestore := make(chan struct{})
	var reactionQueryOnce sync.Once
	var continueOnce sync.Once
	releaseRestoreQuery := func() { continueOnce.Do(func() { close(continueRestore) }) }
	callbackName := "user_like_restore_post_delete_race"
	if err := db.Callback().Row().After("gorm:row").Register(callbackName, func(tx *gorm.DB) {
		tableExpr := ""
		if tx.Statement.TableExpr != nil {
			tableExpr = strings.ToLower(tx.Statement.TableExpr.SQL)
		}
		if strings.EqualFold(tx.Statement.Table, "reaction") || strings.Contains(tableExpr, "post_reaction") {
			reactionQueryOnce.Do(func() { close(reactionQueryReached) })
			<-continueRestore
		}
	}); err != nil {
		t.Fatalf("register Post delete race query gate: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(callbackName) })
	t.Cleanup(releaseRestoreQuery)

	restoreDone := make(chan error, 1)
	restoreFinished := make(chan struct{})
	restoreCtx, cancelRestore := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelRestore()
	go func() {
		defer close(restoreFinished)
		restoreDone <- NewUserLikeLifecycle(store, db).RecoverIfCold(restoreCtx, userID)
	}()
	t.Cleanup(func() {
		releaseRestoreQuery()
		select {
		case <-restoreFinished:
		case <-time.After(5 * time.Second):
			t.Error("restore goroutine did not stop during test cleanup")
		}
	})
	select {
	case <-reactionQueryReached:
	case <-time.After(5 * time.Second):
		select {
		case err := <-restoreDone:
			t.Fatalf("restore exited before the post_reaction query gate: %v", err)
		default:
		}
		t.Fatal("restore did not pause before the post_reaction query")
	}
	if err := db.Exec("UPDATE \""+schemaName+"\".posts SET deleted_at = now() WHERE id = ?", postID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	releaseRestoreQuery()
	select {
	case err := <-restoreDone:
		if !errors.Is(err, ErrUserLikeRecoveryUnsafe) {
			t.Fatalf("restore concurrent with Post deletion error=%v want unsafe", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not finish after releasing the query gate")
	}
	if exists, err := client.Exists(UserLikesKey(userID)).Result(); err != nil || exists != 0 {
		t.Fatalf("concurrent Post deletion installed User Set exists=%d err=%v", exists, err)
	}
	if ready, err := client.Get(ReadyKey(postID)).Result(); err != nil || ready != "deleted" {
		t.Fatalf("restore changed Post deletion fence ready=%q err=%v", ready, err)
	}
}

func TestUserLikeConcurrentMutationsWaitForColdRestoreIntegration(t *testing.T) {
	dsn, redisAddr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR to run User Like restore/Mutation concurrency integration test")
	}
	db := openUserLikeLifecyclePostgresIntegration(t, dsn)
	settings := userLikeIntegrationSettings(true, 300*time.Millisecond)
	client, store, userID, originalPostID := openUserLikeLifecycleRedisIntegration(t, settings)
	newPostIDs := []uint{originalPostID + 1, originalPostID + 2, originalPostID + 3}
	t.Cleanup(func() {
		for _, postID := range newPostIDs {
			cleanupUserLikeLifecyclePostState(client, postID)
		}
	})
	if err := db.Exec("INSERT INTO users (id, deleted_at) VALUES (?, NULL)", userID).Error; err != nil {
		t.Fatal(err)
	}
	for _, postID := range append([]uint{originalPostID}, newPostIDs...) {
		if err := db.Exec("INSERT INTO posts (id, deleted_at, visibility) VALUES (?, NULL, 'private')", postID).Error; err != nil {
			t.Fatalf("insert Post %d: %v", postID, err)
		}
	}
	if err := db.Exec(`INSERT INTO post_reaction (user_id, post_id, reaction, liked, reaction_version, updated_at, state_changed_at)
VALUES (?, ?, 1, TRUE, 5, now(), now())`, userID, originalPostID).Error; err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		ReadyKey(originalPostID): "1", CountKey(originalPostID): "1", VersionKey(originalPostID): "5",
	} {
		if err := client.Set(key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, postID := range newPostIDs {
		for key, value := range map[string]string{ReadyKey(postID): "1", CountKey(postID): "0", VersionKey(postID): "0"} {
			if err := client.Set(key, value, 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(UserLikesKey(userID), strconv.FormatUint(uint64(originalPostID), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	awaitUserLikeSetExpiry(t, client, userID, 2*time.Second)

	reactionQueryReached := make(chan struct{})
	continueRestore := make(chan struct{})
	var queryOnce, releaseOnce sync.Once
	releaseRestoreQuery := func() { releaseOnce.Do(func() { close(continueRestore) }) }
	callbackName := "user_like_restore_mutation_race"
	if err := db.Callback().Row().After("gorm:row").Register(callbackName, func(tx *gorm.DB) {
		tableExpr := ""
		if tx.Statement.TableExpr != nil {
			tableExpr = strings.ToLower(tx.Statement.TableExpr.SQL)
		}
		if strings.EqualFold(tx.Statement.Table, "reaction") || strings.Contains(tableExpr, "post_reaction") {
			queryOnce.Do(func() { close(reactionQueryReached) })
			<-continueRestore
		}
	}); err != nil {
		t.Fatalf("register concurrent mutation query gate: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(callbackName) })
	t.Cleanup(releaseRestoreQuery)

	type mutationResult struct {
		postID uint
		result MutationResult
		err    error
	}
	const requestCount = 3
	start := make(chan struct{})
	recoveryStarted := make(chan struct{}, requestCount)
	results := make(chan mutationResult, requestCount)
	lifecycle := NewUserLikeLifecycle(store, db)
	for _, postID := range newPostIDs {
		go func(postID uint) {
			<-start
			result, err := store.Mutate(context.Background(), userID, postID, true)
			if errors.Is(err, ErrUserLikeNotReady) {
				recoveryStarted <- struct{}{}
				err = lifecycle.RecoverIfCold(context.Background(), userID)
				if err == nil {
					result, err = store.Mutate(context.Background(), userID, postID, true)
				}
			}
			results <- mutationResult{postID: postID, result: result, err: err}
		}(postID)
	}
	close(start)
	select {
	case <-reactionQueryReached:
	case <-time.After(5 * time.Second):
		releaseRestoreQuery()
		t.Fatal("cold restore did not reach its PostgreSQL relation query")
	}
	for range requestCount {
		select {
		case <-recoveryStarted:
		case <-time.After(5 * time.Second):
			releaseRestoreQuery()
			t.Fatal("not all concurrent mutations observed the cold User Set")
		}
	}
	time.Sleep(25 * time.Millisecond)
	releaseRestoreQuery()

	got := make(map[uint]MutationResult, requestCount)
	for range requestCount {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("Like after concurrent cold restore Post=%d: %v", result.postID, result.err)
			}
			if !result.result.Changed || result.result.Count != 1 || result.result.Version != 1 || !result.result.Liked {
				t.Fatalf("concurrent Like result for Post %d=%+v want changed count=1 version=1", result.postID, result.result)
			}
			got[result.postID] = result.result
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent mutation did not finish after restore")
		}
	}
	if len(got) != requestCount {
		t.Fatalf("concurrent mutations completed for %d Posts want %d", len(got), requestCount)
	}
	if cardinality, err := client.SCard(UserLikesKey(userID)).Result(); err != nil || cardinality != requestCount+2 {
		t.Fatalf("restored and mutated relation Set cardinality=%d err=%v want sentinel + original + new Likes", cardinality, err)
	}
	for _, postID := range append([]uint{originalPostID}, newPostIDs...) {
		count, countErr := client.Get(CountKey(postID)).Int64()
		version, versionErr := client.Get(VersionKey(postID)).Int64()
		wantCount, wantVersion := int64(1), int64(1)
		if postID == originalPostID {
			wantCount, wantVersion = 1, 5
		}
		if countErr != nil || versionErr != nil || count != wantCount || version != wantVersion {
			t.Fatalf("Post %d aggregate after restore race count=%d/%v version=%d/%v want=%d/%d", postID, count, countErr, version, versionErr, wantCount, wantVersion)
		}
	}
}

func openUserLikeLifecyclePostgresIntegration(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL integration service: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	schema := fmt.Sprintf("user_like_restore_%d", time.Now().UnixNano())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("SET search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz NULL)",
		"CREATE TABLE posts (id bigint PRIMARY KEY, deleted_at timestamptz NULL, visibility text NOT NULL)",
		"CREATE TABLE post_reaction (user_id bigint NOT NULL, post_id bigint NOT NULL, reaction smallint NOT NULL, liked boolean NOT NULL, reaction_version bigint NOT NULL, updated_at timestamptz NOT NULL, state_changed_at timestamptz NOT NULL, PRIMARY KEY (user_id, post_id))",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create PostgreSQL restore fixture table: %v", err)
		}
	}
	t.Cleanup(func() {
		if err := db.Exec("SET search_path TO public").Error; err != nil {
			t.Errorf("restore integration PostgreSQL search_path: %v", err)
		}
		if err := db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop integration schema %s: %v", schema, err)
		}
	})
	return db
}

func awaitUserLikeSetExpiry(t *testing.T, client *redis.Client, userID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		exists, err := client.Exists(UserLikesKey(userID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("User Like Set did not expire before the integration deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func cleanupUserLikeLifecyclePostState(client *redis.Client, postID uint) {
	post := strconv.FormatUint(uint64(postID), 10)
	_ = client.Del(ReadyKey(postID), CountKey(postID), VersionKey(postID), RebuildTokenKey(postID)).Err()
	_ = client.SRem(DirtyKey, post).Err()
	_ = client.SRem(RegistryKey, post).Err()
	_ = client.ZRem(ExpiryCandidatesKey, post).Err()
	_ = client.HDel(RecoverableVersionsKey, post).Err()
}
