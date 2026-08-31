package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

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

	originalIncrement := incrementPostReplyCount
	originalDetailInvalidation := invalidateReplyPostDetailCache
	incrementPostReplyCount = func(*gorm.DB, uint) (int64, error) {
		return 0, errors.New("forced comment count update failure")
	}
	detailCalls := 0
	invalidateReplyPostDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	t.Cleanup(func() {
		incrementPostReplyCount = originalIncrement
		invalidateReplyPostDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts/"+strconvUint(fixture.Article.ID)+"/replies",
		strconvUint(fixture.Article.ID),
		`{"content":"must roll back"}`,
		fixture.Commenter.ID,
	)
	CreatePostReply(ctx)
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
	originalDetailInvalidation := invalidateReplyPostDetailCache
	detailCalls := 0
	invalidateReplyPostDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	t.Cleanup(func() {
		invalidateReplyPostDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodDelete,
		"/api/posts/"+strconvUint(comment.ID),
		strconvUint(comment.ID),
		"",
		fixture.Commenter.ID,
	)
	DeletePostReply(ctx)
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
	originalDetailInvalidation := invalidateReplyPostDetailCache
	detailCalls, detailPostIDs := 0, make([]uint, 0, 2)
	invalidateReplyPostDetailCache = func(postID uint) error {
		detailCalls++
		detailPostIDs = append(detailPostIDs, postID)
		return errors.New("detail cache unavailable")
	}
	t.Cleanup(func() {
		invalidateReplyPostDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts/"+strconvUint(fixture.Article.ID)+"/replies",
		strconvUint(fixture.Article.ID),
		`{"content":"cache failures are best effort"}`,
		fixture.Commenter.ID,
	)
	CreatePostReply(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after create=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 1 {
		t.Fatalf("create invalidation calls detail=%d", detailCalls)
	}

	var response replyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	invalidateReplyPostDetailCache = func(postID uint) error {
		detailCalls++
		detailPostIDs = append(detailPostIDs, postID)
		return nil
	}
	deleteCtx, deleteRecorder := newReplyIntegrationContext(
		http.MethodDelete,
		"/api/posts/"+strconvUint(response.ID),
		strconvUint(response.ID),
		"",
		fixture.Commenter.ID,
	)
	DeletePostReply(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 2 || len(detailPostIDs) != 2 || detailPostIDs[0] != fixture.Article.ID || detailPostIDs[1] != fixture.Article.ID {
		t.Fatalf("delete invalidation calls detail=%d", detailCalls)
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
		"/api/posts/"+strconvUint(fixture.Article.ID)+"/replies",
		strconvUint(fixture.Article.ID),
		`{"content":"redis nil safe"}`,
		fixture.Commenter.ID,
	)
	CreatePostReply(ctx)
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
	DeletePostReply(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", replyPostCount(t, db, fixture.Article.ID))
	}
}
