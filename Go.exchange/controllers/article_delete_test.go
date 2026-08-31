package controllers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newPostDeleteContext(id string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/posts/"+id, nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func stubPostDeleteDependencies(t *testing.T, transactionErr error) {
	t.Helper()
	originalViewer := loadPostDeleteViewer
	originalTransaction := deletePostInTransaction
	originalDetail := invalidatePostDeleteDetailCache
	originalLikes := cleanupDeletedPostLikeState
	t.Cleanup(func() {
		loadPostDeleteViewer = originalViewer
		deletePostInTransaction = originalTransaction
		invalidatePostDeleteDetailCache = originalDetail
		cleanupDeletedPostLikeState = originalLikes
	})

	loadPostDeleteViewer = func(uint) error { return nil }
	deletePostInTransaction = func(uint, uint) error { return transactionErr }
	invalidatePostDeleteDetailCache = func(uint) error { return nil }
	cleanupDeletedPostLikeState = func(uint) error { return nil }
}

func TestDeletePostRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc"} {
		ctx, recorder := newPostDeleteContext(id, nil)
		DeletePost(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("id=%q status=%d body=%s", id, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDeletePostRejectsMissingAuthContext(t *testing.T) {
	ctx, recorder := newPostDeleteContext("42", nil)
	DeletePost(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeletePostRejectsMissingOrInactiveViewer(t *testing.T) {
	originalViewer := loadPostDeleteViewer
	t.Cleanup(func() { loadPostDeleteViewer = originalViewer })
	loadPostDeleteViewer = func(uint) error { return gorm.ErrRecordNotFound }

	viewerID := uint(7)
	ctx, recorder := newPostDeleteContext("42", &viewerID)
	DeletePost(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeletePostMapsMissingAndForbiddenTransactions(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing", err: errPostDeleteNotFound, want: http.StatusNotFound},
		{name: "forbidden", err: errPostDeleteForbidden, want: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubPostDeleteDependencies(t, testCase.err)
			ctx, recorder := newPostDeleteContext("42", &viewerID)
			DeletePost(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if testCase.want == http.StatusNotFound && recorder.Body.String() != "{\"error\":\"post not found\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
			if testCase.want == http.StatusForbidden && recorder.Body.String() != "{\"error\":\"forbidden\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
}

func TestDeletePostOwnerReturnsNoContentAndCleansUpOnce(t *testing.T) {
	viewerID := uint(7)
	var detailCalls, likeCalls int
	stubPostDeleteDependencies(t, nil)
	invalidatePostDeleteDetailCache = func(uint) error {
		detailCalls++
		return errors.New("detail cache unavailable")
	}
	cleanupDeletedPostLikeState = func(uint) error {
		likeCalls++
		return errors.New("redis unavailable")
	}

	ctx, recorder := newPostDeleteContext("42", &viewerID)
	DeletePost(ctx)
	ctx.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if detailCalls != 1 || likeCalls != 1 {
		t.Fatalf("cleanup calls detail=%d likes=%d", detailCalls, likeCalls)
	}
}

func TestDeletePostDoesNotCleanUpRejectedRequest(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: errPostDeleteNotFound},
		{name: "forbidden", err: errPostDeleteForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubPostDeleteDependencies(t, testCase.err)
			var calls int

			invalidatePostDeleteDetailCache = func(uint) error { calls++; return nil }
			cleanupDeletedPostLikeState = func(uint) error { calls++; return nil }
			ctx, _ := newPostDeleteContext("42", &viewerID)
			DeletePost(ctx)
			if calls != 0 {
				t.Fatalf("cleanup calls=%d", calls)
			}
		})
	}
}

func openPostDeleteIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDeletePostIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostDeleteIntegrationDatabase(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	owner := models.User{Username: "delete-owner-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "delete-other-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("author_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, other.ID}).Delete(&models.User{})
	})

	forbiddenArticle := models.Post{AuthorID: owner.ID, Content: "forbidden body", Visibility: "public"}
	if err := db.Create(&forbiddenArticle).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newPostDeleteContext(strconvPostID(forbiddenArticle.ID), &other.ID)
	DeletePost(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	article := models.Post{AuthorID: owner.ID, Content: "delete fixture body", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostRepost{UserID: other.ID, PostID: article.ID}).Error; err != nil {
		t.Fatal(err)
	}
	deleteOne := func(postID, viewerID uint) int {
		ctx, recorder := newPostDeleteContext(strconvPostID(postID), &viewerID)
		DeletePost(ctx)
		ctx.Writer.WriteHeaderNow()
		return recorder.Code
	}
	if status := deleteOne(article.ID, owner.ID); status != http.StatusNoContent {
		t.Fatalf("article=%d status=%d", article.ID, status)
	}
	if status := deleteOne(article.ID, owner.ID); status != http.StatusNotFound {
		t.Fatalf("repeat delete status=%d", status)
	}
	var deleted models.Post
	if err := db.Unscoped().First(&deleted, article.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft deleted article=%#v err=%v", deleted, err)
	}
	var remainingReposts int64
	if err := db.Model(&models.PostRepost{}).Where("post_id = ?", article.ID).Count(&remainingReposts).Error; err != nil {
		t.Fatal(err)
	}
	if remainingReposts != 0 {
		t.Fatalf("reposts remaining after article delete=%d", remainingReposts)
	}

	raceArticle := models.Post{AuthorID: owner.ID, Content: "race body", Visibility: "public"}
	if err := db.Create(&raceArticle).Error; err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() { defer waitGroup.Done(); statuses <- deleteOne(raceArticle.ID, owner.ID) }()
	}
	waitGroup.Wait()
	close(statuses)
	successes, notFound := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			successes++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("race status=%d", status)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("race successes=%d notFound=%d", successes, notFound)
	}
}
func strconvPostID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
