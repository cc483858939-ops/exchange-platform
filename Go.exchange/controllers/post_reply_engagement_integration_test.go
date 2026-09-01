package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func softDeleteCounterAwareComment(t *testing.T, db *gorm.DB, comment models.Post) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&comment)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture comment delete affected an unexpected number of rows")
		}
		result = tx.Model(&models.Post{}).
			Where("id = ? AND reply_count > 0", *comment.ReplyToPostID).
			UpdateColumn("reply_count", gorm.Expr("reply_count - 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture post counter decrement affected an unexpected number of rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func replyPostCount(t *testing.T, db *gorm.DB, postID uint) int64 {
	t.Helper()
	var post models.Post
	if err := db.Select("reply_count").First(&post, postID).Error; err != nil {
		t.Fatal(err)
	}
	return post.ReplyCount
}

func TestReplyCountTransactionRollbackIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	if err := db.AutoMigrate(&models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}

	originalIncrement := incrementPostReplyCount
	originalDetailInvalidation := invalidatePostCreateParentDetailCache
	originalInitialize := initializePostLikeState
	incrementPostReplyCount = func(*gorm.DB, uint) (int64, error) {
		return 0, errors.New("forced comment count update failure")
	}
	detailCalls := 0
	invalidatePostCreateParentDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	initializeCalls := 0
	initializePostLikeState = func(uint) error {
		initializeCalls++
		return nil
	}
	t.Cleanup(func() {
		incrementPostReplyCount = originalIncrement
		invalidatePostCreateParentDetailCache = originalDetailInvalidation
		initializePostLikeState = originalInitialize
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts",
		strconvUint(fixture.Article.ID),
		`{"content":"must roll back","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var posts int64
	if err := db.Model(&models.Post{}).
		Where("reply_to_post_id = ? AND content = ?", fixture.Article.ID, "must roll back").
		Count(&posts).Error; err != nil {
		t.Fatal(err)
	}
	if posts != 0 || replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("rollback posts=%d comment_count=%d", posts, replyPostCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 0 {
		t.Fatalf("cache invalidated after rolled-back create: detail=%d", detailCalls)
	}
	if initializeCalls != 0 {
		t.Fatalf("like state initialized after rolled-back create: calls=%d", initializeCalls)
	}
	var behaviorCount int64
	if err := db.Model(&models.PostBehavior{}).Where("user_id = ? AND post_id = ? AND action = ?", fixture.Commenter.ID, fixture.Article.ID, PostBehaviorActionReply).Count(&behaviorCount).Error; err != nil {
		t.Fatal(err)
	}
	if behaviorCount != 0 {
		t.Fatalf("reply behavior rows after rollback=%d", behaviorCount)
	}
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).Where("event_type = ? AND message::text LIKE ?", eventing.EventTypeReplyCreated, "%must roll back%").Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("reply outbox rows after rollback=%d", outboxCount)
	}
}

func TestReplyCountUnderflowRollsBackIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	conversationID := fixture.Article.ID
	comment := models.Post{AuthorID: fixture.Commenter.ID, ReplyToPostID: &fixture.Article.ID, ConversationID: &conversationID, Content: "legacy inconsistent reply", Visibility: "public"}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatal("fixture should begin with a zero comment count")
	}
	originalDetailInvalidation := invalidatePostDeleteDetailCache
	detailCalls := 0
	invalidatePostDeleteDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	t.Cleanup(func() {
		invalidatePostDeleteDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodDelete,
		"/api/posts/"+strconvUint(comment.ID),
		strconvUint(comment.ID),
		"",
		fixture.Commenter.ID,
	)
	DeletePost(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if err := db.First(&models.Post{}, comment.ID).Error; err != nil {
		t.Fatalf("comment should remain after transactional rollback: %v", err)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("comment count changed despite underflow rollback: %d", replyPostCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 0 {
		t.Fatalf("cache invalidated after rolled-back delete: detail=%d", detailCalls)
	}
}

func TestReplyCacheInvalidationIsBestEffortIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	originalCreateDetailInvalidation := invalidatePostCreateParentDetailCache
	originalDeleteDetailInvalidation := invalidatePostDeleteDetailCache
	createDetailCalls, createDetailPostIDs := 0, make([]uint, 0, 1)
	invalidatePostCreateParentDetailCache = func(postID uint) error {
		createDetailCalls++
		createDetailPostIDs = append(createDetailPostIDs, postID)
		return errors.New("detail cache unavailable")
	}
	deleteDetailCalls, deleteDetailPostIDs := 0, make([]uint, 0, 2)
	invalidatePostDeleteDetailCache = func(postID uint) error {
		deleteDetailCalls++
		deleteDetailPostIDs = append(deleteDetailPostIDs, postID)
		return errors.New("detail cache unavailable")
	}
	t.Cleanup(func() {
		invalidatePostCreateParentDetailCache = originalCreateDetailInvalidation
		invalidatePostDeleteDetailCache = originalDeleteDetailInvalidation
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts",
		strconvUint(fixture.Article.ID),
		`{"content":"cache failures are best effort","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after create=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	if createDetailCalls != 1 || len(createDetailPostIDs) != 1 || createDetailPostIDs[0] != fixture.Article.ID {
		t.Fatalf("create invalidation calls=%d ids=%v", createDetailCalls, createDetailPostIDs)
	}

	var response replyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	deleteCtx, deleteRecorder := newReplyIntegrationContext(
		http.MethodDelete,
		"/api/posts/"+strconvUint(response.ID),
		strconvUint(response.ID),
		"",
		fixture.Commenter.ID,
	)
	DeletePost(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	if deleteDetailCalls != 2 || len(deleteDetailPostIDs) != 2 || deleteDetailPostIDs[0] != response.ID || deleteDetailPostIDs[1] != fixture.Article.ID {
		t.Fatalf("delete invalidation calls=%d ids=%v", deleteDetailCalls, deleteDetailPostIDs)
	}
}

func TestReplyMutationIsRedisNilSafeIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	originalRedis := global.RedisDB
	global.RedisDB = nil
	t.Cleanup(func() { global.RedisDB = originalRedis })

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts",
		strconvUint(fixture.Article.ID),
		`{"content":"redis nil safe","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after create=%d", replyPostCount(t, db, fixture.Article.ID))
	}

	var created replyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	deleteCtx, deleteRecorder := newReplyIntegrationContext(
		http.MethodDelete,
		"/api/posts/"+strconvUint(created.ID),
		strconvUint(created.ID),
		"",
		fixture.Commenter.ID,
	)
	DeletePost(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", replyPostCount(t, db, fixture.Article.ID))
	}
}

func TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration(t *testing.T) {
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	redisClient := openPostLikeIntegrationRedis(t)

	initial, err := loadPostDetail(strconvUint(fixture.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if initial.ReplyCount != 0 {
		t.Fatalf("initial reply count=%d", initial.ReplyCount)
	}

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID),
		`{"content":"cache-visible reply","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupPostLikeIntegrationState(redisClient, []uint{created.ID}, []uint{fixture.Author.ID, fixture.Commenter.ID, fixture.Other.ID})
	})

	reloaded, err := loadPostDetail(strconvUint(fixture.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ReplyCount != 1 {
		t.Fatalf("reloaded reply count=%d want 1", reloaded.ReplyCount)
	}
	cachedCountOne, err := loadPostDetail(strconvUint(fixture.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if cachedCountOne.ReplyCount != 1 {
		t.Fatalf("cached reply count=%d want 1", cachedCountOne.ReplyCount)
	}

	deleteCtx, deleteRecorder := newReplyIntegrationContext(
		http.MethodDelete, "/api/posts/"+strconvUint(created.ID), strconvUint(created.ID), "", fixture.Commenter.ID,
	)
	DeletePost(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	reloadedAfterDelete, err := loadPostDetail(strconvUint(fixture.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if reloadedAfterDelete.ReplyCount != 0 {
		t.Fatalf("reloaded reply count after delete=%d want 0", reloadedAfterDelete.ReplyCount)
	}
	cachedCountZero, err := loadPostDetail(strconvUint(fixture.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if cachedCountZero.ReplyCount != 0 {
		t.Fatalf("cached reply count after delete=%d want 0", cachedCountZero.ReplyCount)
	}

	replyCtx, replyRecorder := newReplyIntegrationContext(
		http.MethodGet, "/api/posts/"+strconvUint(created.ID), strconvUint(created.ID), "", fixture.Commenter.ID,
	)
	GetPostByID(replyCtx)
	if replyRecorder.Code != http.StatusNotFound {
		t.Fatalf("deleted reply detail status=%d body=%s", replyRecorder.Code, replyRecorder.Body.String())
	}
	var parent models.Post
	if err := db.First(&parent, fixture.Article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if parent.DeletedAt.Valid || parent.ReplyCount != 0 {
		t.Fatalf("parent after reply delete=%#v", parent)
	}
}

func TestCanonicalDeleteReplyDecrementsSoftDeletedParentIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID),
		`{"content":"reply under parent tombstone","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&fixture.Article).Error; err != nil {
		t.Fatal(err)
	}

	deleteCtx, deleteRecorder := newReplyIntegrationContext(
		http.MethodDelete, "/api/posts/"+strconvUint(created.ID), strconvUint(created.ID), "", fixture.Commenter.ID,
	)
	DeletePost(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	var parent models.Post
	if err := db.Unscoped().First(&parent, fixture.Article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !parent.DeletedAt.Valid || parent.ReplyCount != 0 {
		t.Fatalf("parent after child delete=%#v", parent)
	}
	var deletedReply models.Post
	if err := db.Unscoped().First(&deletedReply, created.ID).Error; err != nil || !deletedReply.DeletedAt.Valid {
		t.Fatalf("reply after delete=%#v err=%v", deletedReply, err)
	}
}

func TestCanonicalReplyCreateAndParentDeleteSerializeIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	if err := db.AutoMigrate(&models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}

	raceStarted := time.Now().UTC()
	barrier := make(chan struct{})
	createDone := make(chan *httptest.ResponseRecorder, 1)
	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		<-barrier
		ctx, recorder := newReplyIntegrationContext(
			http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID),
			`{"content":"racing reply","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
			fixture.Commenter.ID,
		)
		createPost(ctx, nil)
		createDone <- recorder
	}()
	go func() {
		<-barrier
		ctx, recorder := newReplyIntegrationContext(
			http.MethodDelete, "/api/posts/"+strconvUint(fixture.Article.ID), strconvUint(fixture.Article.ID), "", fixture.Author.ID,
		)
		DeletePost(ctx)
		deleteDone <- recorder
	}()
	close(barrier)
	createRecorder := <-createDone
	deleteRecorder := <-deleteDone
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("parent delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	var parent models.Post
	if err := db.Unscoped().First(&parent, fixture.Article.ID).Error; err != nil {
		t.Fatal(err)
	}
	var replies []models.Post
	if err := db.Unscoped().Where("reply_to_post_id = ?", fixture.Article.ID).Find(&replies).Error; err != nil {
		t.Fatal(err)
	}
	var behaviorCount int64
	if err := db.Model(&models.PostBehavior{}).Where("user_id = ? AND post_id = ? AND action = ?", fixture.Commenter.ID, fixture.Article.ID, PostBehaviorActionReply).Count(&behaviorCount).Error; err != nil {
		t.Fatal(err)
	}
	var outboxCount int64
	outboxQuery := db.Model(&models.OutboxEvent{}).Where("event_type = ?", eventing.EventTypeReplyCreated)
	if len(replies) == 1 {
		outboxQuery = outboxQuery.Where("aggregate_id = ?", strconvUint(replies[0].ID))
	} else {
		outboxQuery = outboxQuery.Where("created_at >= ?", raceStarted)
	}
	if err := outboxQuery.Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}

	switch createRecorder.Code {
	case http.StatusCreated:
		if len(replies) != 1 || replies[0].DeletedAt.Valid || parent.ReplyCount != 1 || behaviorCount != 1 || outboxCount != 1 {
			t.Fatalf("serialized success parent=%#v replies=%#v behavior=%d outbox=%d", parent, replies, behaviorCount, outboxCount)
		}
	case http.StatusNotFound:
		if len(replies) != 0 || parent.ReplyCount != 0 || behaviorCount != 0 || outboxCount != 0 {
			t.Fatalf("serialized rejection parent=%#v replies=%#v behavior=%d outbox=%d", parent, replies, behaviorCount, outboxCount)
		}
	default:
		t.Fatalf("reply create status=%d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	if !parent.DeletedAt.Valid {
		t.Fatalf("parent was not soft deleted: %#v", parent)
	}
}
