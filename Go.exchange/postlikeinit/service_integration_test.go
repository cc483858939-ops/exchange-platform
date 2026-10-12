package postlikeinit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestRunPassRecoversPendingJobAndPreservesReadyStateIntegration(t *testing.T) {
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
	schema := fmt.Sprintf("post_like_init_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}, &models.Post{}, &models.PostReaction{}, &models.PostLikeInitJob{}); err != nil {
		t.Fatal(err)
	}
	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: redisDB})
	t.Cleanup(func() { client.Close() })
	store := likes.NewStore(client)

	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	user := models.User{Username: fmt.Sprintf("post-like-init-%d", postID), Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{Model: gorm.Model{ID: postID}, AuthorID: user.ID, Content: "retryable post", Visibility: "public"}
	if err := tx.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	cleanupRedis := func(id uint) {
		_ = store.DeletePost(context.Background(), id)
		client.Del(likes.ReadyKey(id), likes.CountKey(id), likes.VersionKey(id), likes.UsersKey(id), likes.RebuildTokenKey(id))
		client.SRem(likes.RegistryKey, id)
		client.ZRem(likes.ExpiryCandidatesKey, id)
	}
	t.Cleanup(func() { cleanupRedis(postID) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := models.PostLikeInitJob{PostID: postID, RetryAfter: now, State: models.PostLikeInitJobPending}
	if err := tx.Create(&job).Error; err != nil {
		t.Fatal(err)
	}

	if err := RunPass(context.Background(), tx, nil, now); err == nil {
		t.Fatal("RunPass with unavailable Redis must leave a retryable job")
	}
	var delayed models.PostLikeInitJob
	if err := tx.First(&delayed, "post_id = ?", postID).Error; err != nil {
		t.Fatal(err)
	}
	if delayed.State != models.PostLikeInitJobPending || !delayed.RetryAfter.After(now) {
		t.Fatalf("outage task=%+v want pending with backoff", delayed)
	}
	if err := RunPass(context.Background(), tx, store, delayed.RetryAfter.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadFullState(context.Background(), postID); err != nil {
		t.Fatalf("recovered Redis state: %v", err)
	}
	if err := tx.First(&delayed, "post_id = ?", postID).Error; err == nil {
		t.Fatal("successful task was not acknowledged")
	}

	readyPostID := postID + 1
	readyPost := models.Post{Model: gorm.Model{ID: readyPostID}, AuthorID: user.ID, Content: "already initialized", Visibility: "public"}
	if err := tx.Create(&readyPost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRedis(readyPostID) })
	for key, value := range map[string]string{
		likes.ReadyKey(readyPostID):   "1",
		likes.CountKey(readyPostID):   "9",
		likes.VersionKey(readyPostID): "17",
	} {
		if err := client.Set(key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Create(&models.PostLikeInitJob{PostID: readyPostID, RetryAfter: now, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunPass(context.Background(), tx, store, now); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadFullState(context.Background(), readyPostID)
	if err != nil || state.Count != 9 || state.Version != 17 {
		t.Fatalf("ready state=%+v err=%v want count=9 version=17", state, err)
	}
	var acknowledged models.PostLikeInitJob
	if err := tx.First(&acknowledged, "post_id = ?", readyPostID).Error; err == nil {
		t.Fatal("already-ready task was not acknowledged")
	}

	unsafePostID := readyPostID + 1
	unsafePost := models.Post{Model: gorm.Model{ID: unsafePostID}, AuthorID: user.ID, Content: "nonzero baseline", Visibility: "public", LikeCount: 1}
	if err := tx.Create(&unsafePost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRedis(unsafePostID) })
	if err := tx.Create(&models.PostLikeInitJob{PostID: unsafePostID, RetryAfter: now, Attempts: 10, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunPass(context.Background(), tx, store, now); err == nil {
		t.Fatal("unsafe SQL baseline must be retained and reported")
	}
	var unsafeJob models.PostLikeInitJob
	if err := tx.First(&unsafeJob, "post_id = ?", unsafePostID).Error; err != nil {
		t.Fatal(err)
	}
	if unsafeJob.State != models.PostLikeInitJobAttempted {
		t.Fatalf("unsafe task state=%q want attempted", unsafeJob.State)
	}
	if _, err := store.LoadFullState(context.Background(), unsafePostID); err == nil {
		t.Fatal("unsafe SQL baseline was initialized in Redis")
	}

	deletedPostID := unsafePostID + 1
	deletedPost := models.Post{Model: gorm.Model{ID: deletedPostID}, AuthorID: user.ID, Content: "deleted before retry", Visibility: "public"}
	if err := tx.Create(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRedis(deletedPostID) })
	if err := tx.Create(&models.PostLikeInitJob{PostID: deletedPostID, RetryAfter: now, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Unscoped().Model(&models.Post{}).Where("id = ?", deletedPostID).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunPass(context.Background(), tx, store, now); err != nil {
		t.Fatalf("deleted Post task cleanup: %v", err)
	}
	var deletedJob models.PostLikeInitJob
	if err := tx.First(&deletedJob, "post_id = ?", deletedPostID).Error; err == nil {
		t.Fatal("deleted Post initialization task was not acknowledged")
	}
	if exists, err := client.Exists(likes.ReadyKey(deletedPostID)).Result(); err != nil || exists != 0 {
		t.Fatalf("deleted Post Redis state exists=%d err=%v", exists, err)
	}

	partialPostID := deletedPostID + 1
	partialPost := models.Post{Model: gorm.Model{ID: partialPostID}, AuthorID: user.ID, Content: "partial Redis state", Visibility: "public"}
	if err := tx.Create(&partialPost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRedis(partialPostID) })
	if err := tx.Create(&models.PostLikeInitJob{PostID: partialPostID, RetryAfter: now, Attempts: 10, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.ReadyKey(partialPostID), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(likes.CountKey(partialPostID), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadFullState(context.Background(), partialPostID); !errors.Is(err, likes.ErrLikeRedisType) {
		t.Fatalf("wrong-type Redis state error=%v want ErrLikeRedisType", err)
	}
	if err := RunPass(context.Background(), tx, store, now); err == nil {
		t.Fatal("wrong-type Redis state must stay fail-closed")
	}
	var partialJob models.PostLikeInitJob
	if err := tx.First(&partialJob, "post_id = ?", partialPostID).Error; err != nil {
		t.Fatal(err)
	}
	if partialJob.State != models.PostLikeInitJobAttempted {
		t.Fatalf("partial Redis task state=%q want attempted", partialJob.State)
	}
	if keyType, err := client.Type(likes.CountKey(partialPostID)).Result(); err != nil || keyType != "list" {
		t.Fatalf("partial Redis count key type=%q err=%v want list unchanged", keyType, err)
	}

	ackPostID := partialPostID + 1
	ackPost := models.Post{Model: gorm.Model{ID: ackPostID}, AuthorID: user.ID, Content: "acknowledgement retry", Visibility: "public"}
	if err := tx.Create(&ackPost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRedis(ackPostID) })
	if err := tx.Create(&models.PostLikeInitJob{PostID: ackPostID, RetryAfter: now, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	functionName := fmt.Sprintf("skip_post_like_init_ack_%d", ackPostID)
	triggerName := fmt.Sprintf("skip_post_like_init_ack_trigger_%d", ackPostID)
	createFunction := fmt.Sprintf(
		"CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.post_id = %d THEN RETURN NULL; END IF; RETURN OLD; END; $$",
		functionName, ackPostID,
	)
	if err := tx.Exec(createFunction).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE DELETE ON post_like_init_jobs FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName)).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunPass(context.Background(), tx, store, now); err == nil {
		t.Fatal("suppressed task acknowledgement must be reported")
	}
	var ackJob models.PostLikeInitJob
	if err := tx.First(&ackJob, "post_id = ?", ackPostID).Error; err != nil {
		t.Fatalf("durable task after acknowledgement failure: %v", err)
	}
	if ackJob.State != models.PostLikeInitJobAttempted {
		t.Fatalf("acknowledgement failure task state=%q want attempted", ackJob.State)
	}
	if err := client.Set(likes.CountKey(ackPostID), "9", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(likes.VersionKey(ackPostID), "17", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(fmt.Sprintf("DROP TRIGGER %s ON post_like_init_jobs", triggerName)).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(fmt.Sprintf("DROP FUNCTION %s()", functionName)).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunPass(context.Background(), tx, store, ackJob.RetryAfter.Add(time.Microsecond)); err != nil {
		t.Fatalf("retry acknowledgement for already-ready state: %v", err)
	}
	ackState, err := store.LoadFullState(context.Background(), ackPostID)
	if err != nil || ackState.Count != 9 || ackState.Version != 17 {
		t.Fatalf("state after task acknowledgement retry=%+v err=%v, want count=9 version=17", ackState, err)
	}
	if err := tx.First(&ackJob, "post_id = ?", ackPostID).Error; err == nil {
		t.Fatal("task was not acknowledged after PostgreSQL recovered")
	}
}

func TestClaimSkipsJobLockedByAnotherWorkerIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("post_like_init_lock_%d", time.Now().UnixNano())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	if err := db.Connection(func(connDB *gorm.DB) error {
		tx := connDB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.AutoMigrate(&models.PostLikeInitJob{}); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit().Error
	}); err != nil {
		t.Fatal(err)
	}
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Connection(func(connDB *gorm.DB) error {
		tx := connDB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Create(&models.PostLikeInitJob{PostID: postID, RetryAfter: now, State: models.PostLikeInitJobPending}).Error; err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit().Error
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := db.Connection(func(firstConn *gorm.DB) error {
		firstTx := firstConn.WithContext(ctx).Begin()
		if firstTx.Error != nil {
			return firstTx.Error
		}
		if err := firstTx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			firstTx.Rollback()
			return err
		}
		var locked models.PostLikeInitJob
		if err := firstTx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("post_id = ?", postID).Take(&locked).Error; err != nil {
			firstTx.Rollback()
			return err
		}
		claimResult := make(chan struct {
			claimed bool
			err     error
		}, 1)
		go func() {
			claimErr := db.Connection(func(secondConn *gorm.DB) error {
				secondTx := secondConn.WithContext(ctx).Begin()
				if secondTx.Error != nil {
					return secondTx.Error
				}
				if err := secondTx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
					secondTx.Rollback()
					return err
				}
				_, claimed, err := claimPost(ctx, secondTx, postID, now)
				if rollbackErr := secondTx.Rollback().Error; err == nil {
					err = rollbackErr
				}
				claimResult <- struct {
					claimed bool
					err     error
				}{claimed: claimed, err: err}
				return nil
			})
			if claimErr != nil {
				claimResult <- struct {
					claimed bool
					err     error
				}{err: claimErr}
			}
		}()
		select {
		case result := <-claimResult:
			if result.err != nil {
				firstTx.Rollback()
				return result.err
			}
			if result.claimed {
				firstTx.Rollback()
				return fmt.Errorf("second worker claimed a row locked by the first worker")
			}
		case <-ctx.Done():
			firstTx.Rollback()
			return ctx.Err()
		}
		return firstTx.Commit().Error
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Connection(func(connDB *gorm.DB) error {
		tx := connDB.WithContext(ctx).Begin()
		if tx.Error != nil {
			return tx.Error
		}
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			tx.Rollback()
			return err
		}
		_, claimed, err := claimPost(ctx, tx, postID, now)
		if err != nil {
			tx.Rollback()
			return err
		}
		if !claimed {
			tx.Rollback()
			return fmt.Errorf("row was not claimable after the first worker released its lock")
		}
		return tx.Commit().Error
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDuringInitializationPreventsRedisReactivationIntegration(t *testing.T) {
	dsn, addr := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set POSTGRES_TEST_DSN and REDIS_TEST_ADDR")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostReaction{}, &models.PostLikeInitJob{}); err != nil {
		t.Fatal(err)
	}
	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: redisDB})
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	store := likes.NewStore(client)

	user := models.User{Username: fmt.Sprintf("post-like-init-delete-%d", time.Now().UnixNano()), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: user.ID, Content: "delete while initialization is waiting", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Create(&models.PostLikeInitJob{PostID: post.ID, RetryAfter: now, State: models.PostLikeInitJobPending}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Del(likes.ReadyKey(post.ID), likes.CountKey(post.ID), likes.VersionKey(post.ID), likes.RebuildTokenKey(post.ID)).Err()
		_ = db.Unscoped().Where("post_id = ?", post.ID).Delete(&models.PostLikeInitJob{}).Error
		_ = db.Unscoped().Delete(&post).Error
		_ = db.Unscoped().Delete(&user).Error
	})

	deleteTx := db.Begin()
	if deleteTx.Error != nil {
		t.Fatal(deleteTx.Error)
	}
	var lockedPost models.Post
	if err := deleteTx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedPost, post.ID).Error; err != nil {
		deleteTx.Rollback()
		t.Fatal(err)
	}

	workerCtx, cancelWorker := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancelWorker()
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunPass(workerCtx, db, store, now.Add(time.Microsecond))
	}()

	lockWaitDeadline := time.Now().Add(4 * time.Second)
	waitingForPostLock := false
	for time.Now().Before(lockWaitDeadline) {
		var waiting bool
		if err := db.Raw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
			  AND query ILIKE '%"posts"%' AND query ILIKE '%FOR UPDATE%'
		)`).Scan(&waiting).Error; err != nil {
			deleteTx.Rollback()
			t.Fatal(err)
		}
		if waiting {
			waitingForPostLock = true
			break
		}
		select {
		case err := <-workerDone:
			deleteTx.Rollback()
			t.Fatalf("worker finished before it waited for the Post lock: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waitingForPostLock {
		deleteTx.Rollback()
		cancelWorker()
		<-workerDone
		t.Fatal("worker did not block on the Post row lock before deletion")
	}

	if err := deleteTx.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Update("deleted_at", now).Error; err != nil {
		deleteTx.Rollback()
		t.Fatal(err)
	}
	if err := deleteTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatalf("worker cleanup after Post deletion: %v", err)
		}
	case <-workerCtx.Done():
		t.Fatal("worker did not finish after the deleted Post lock was released")
	}

	var remainingJob int64
	if err := db.Model(&models.PostLikeInitJob{}).Where("post_id = ?", post.ID).Count(&remainingJob).Error; err != nil {
		t.Fatal(err)
	}
	if remainingJob != 0 {
		t.Fatalf("deleted Post left %d initialization jobs", remainingJob)
	}
	if exists, err := client.Exists(likes.ReadyKey(post.ID), likes.CountKey(post.ID), likes.VersionKey(post.ID)).Result(); err != nil || exists != 0 {
		t.Fatalf("deleted Post Redis state keys=%d err=%v, want no initialized state", exists, err)
	}
}
