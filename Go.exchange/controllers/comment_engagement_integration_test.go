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

func softDeleteCounterAwareComment(t *testing.T, db *gorm.DB, comment models.Comment) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&comment)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture comment delete affected an unexpected number of rows")
		}
		result = tx.Model(&models.Article{}).
			Where("id = ? AND comment_count > 0", comment.ArticleID).
			UpdateColumn("comment_count", gorm.Expr("comment_count - 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture article counter decrement affected an unexpected number of rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func commentArticleCount(t *testing.T, db *gorm.DB, articleID uint) int64 {
	t.Helper()
	var article models.Article
	if err := db.Select("comment_count").First(&article, articleID).Error; err != nil {
		t.Fatal(err)
	}
	return article.CommentCount
}

func TestCommentCountTransactionRollbackIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	originalIncrement := incrementArticleCommentCount
	originalDetailInvalidation := invalidateCommentArticleDetailCache
	incrementArticleCommentCount = func(*gorm.DB, uint) (int64, error) {
		return 0, errors.New("forced comment count update failure")
	}
	detailCalls := 0
	invalidateCommentArticleDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	t.Cleanup(func() {
		incrementArticleCommentCount = originalIncrement
		invalidateCommentArticleDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newCommentIntegrationContext(
		http.MethodPost,
		"/api/articles/"+strconvUint(fixture.Article.ID)+"/comments",
		strconvUint(fixture.Article.ID),
		`{"content":"must roll back"}`,
		fixture.Commenter.ID,
	)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var comments int64
	if err := db.Model(&models.Comment{}).
		Where("article_id = ? AND content = ?", fixture.Article.ID, "must roll back").
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 0 || commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("rollback comments=%d comment_count=%d", comments, commentArticleCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 0 {
		t.Fatalf("cache invalidated after rolled-back create: detail=%d", detailCalls)
	}
}

func TestCommentCountUnderflowRollsBackIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	comment := models.Comment{ArticleID: fixture.Article.ID, UserID: fixture.Commenter.ID, Content: "legacy inconsistent comment"}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatal("fixture should begin with a zero comment count")
	}
	originalDetailInvalidation := invalidateCommentArticleDetailCache
	detailCalls := 0
	invalidateCommentArticleDetailCache = func(uint) error {
		detailCalls++
		return nil
	}
	t.Cleanup(func() {
		invalidateCommentArticleDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newCommentIntegrationContext(
		http.MethodDelete,
		"/api/comments/"+strconvUint(comment.ID),
		strconvUint(comment.ID),
		"",
		fixture.Commenter.ID,
	)
	DeleteComment(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if err := db.First(&models.Comment{}, comment.ID).Error; err != nil {
		t.Fatalf("comment should remain after transactional rollback: %v", err)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("comment count changed despite underflow rollback: %d", commentArticleCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 0 {
		t.Fatalf("cache invalidated after rolled-back delete: detail=%d", detailCalls)
	}
}

func TestCommentCacheInvalidationIsBestEffortIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	originalDetailInvalidation := invalidateCommentArticleDetailCache
	detailCalls, detailArticleIDs := 0, make([]uint, 0, 2)
	invalidateCommentArticleDetailCache = func(articleID uint) error {
		detailCalls++
		detailArticleIDs = append(detailArticleIDs, articleID)
		return errors.New("detail cache unavailable")
	}
	t.Cleanup(func() {
		invalidateCommentArticleDetailCache = originalDetailInvalidation
	})

	ctx, recorder := newCommentIntegrationContext(
		http.MethodPost,
		"/api/articles/"+strconvUint(fixture.Article.ID)+"/comments",
		strconvUint(fixture.Article.ID),
		`{"content":"cache failures are best effort"}`,
		fixture.Commenter.ID,
	)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after create=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 1 {
		t.Fatalf("create invalidation calls detail=%d", detailCalls)
	}

	var response commentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	invalidateCommentArticleDetailCache = func(articleID uint) error {
		detailCalls++
		detailArticleIDs = append(detailArticleIDs, articleID)
		return nil
	}
	deleteCtx, deleteRecorder := newCommentIntegrationContext(
		http.MethodDelete,
		"/api/comments/"+strconvUint(response.ID),
		strconvUint(response.ID),
		"",
		fixture.Commenter.ID,
	)
	DeleteComment(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
	if detailCalls != 2 || len(detailArticleIDs) != 2 || detailArticleIDs[0] != fixture.Article.ID || detailArticleIDs[1] != fixture.Article.ID {
		t.Fatalf("delete invalidation calls detail=%d", detailCalls)
	}
}

func TestCommentMutationIsRedisNilSafeIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	originalRedis := global.RedisDB
	global.RedisDB = nil
	t.Cleanup(func() { global.RedisDB = originalRedis })

	ctx, recorder := newCommentIntegrationContext(
		http.MethodPost,
		"/api/articles/"+strconvUint(fixture.Article.ID)+"/comments",
		strconvUint(fixture.Article.ID),
		`{"content":"redis nil safe"}`,
		fixture.Commenter.ID,
	)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after create=%d", commentArticleCount(t, db, fixture.Article.ID))
	}

	var created commentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	deleteCtx, deleteRecorder := newCommentIntegrationContext(
		http.MethodDelete,
		"/api/comments/"+strconvUint(created.ID),
		strconvUint(created.ID),
		"",
		fixture.Commenter.ID,
	)
	DeleteComment(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("count after delete=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
}
