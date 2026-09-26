package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/global"

	"github.com/gin-gonic/gin"
)

type requestContextTestKey struct{}

func TestGetPostByIDPassesRequestContextToDetailLoader(t *testing.T) {
	originalCacheLoader := loadPostDetailCache
	originalDB := global.Db
	t.Cleanup(func() {
		loadPostDetailCache = originalCacheLoader
		global.Db = originalDB
	})
	global.Db = nil

	var receivedContext context.Context
	publishedAt := time.Now().UTC()
	loadPostDetailCache = func(ctx context.Context, _ string, _ func() (postResponse, error)) (postResponse, error) {
		receivedContext = ctx
		return postResponse{ID: 42, PublishedAt: &publishedAt, Visibility: "public"}, nil
	}

	requestContext := context.WithValue(context.Background(), requestContextTestKey{}, "detail")
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/posts/42", nil).WithContext(requestContext)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	GetPostByID(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if receivedContext != requestContext || receivedContext.Value(requestContextTestKey{}) != "detail" {
		t.Fatal("detail loader did not receive the original request context")
	}
}

func TestGetPostByIDSuppressesResponseWhenRequestIsCanceled(t *testing.T) {
	originalCacheLoader := loadPostDetailCache
	originalDB := global.Db
	t.Cleanup(func() {
		loadPostDetailCache = originalCacheLoader
		global.Db = originalDB
	})
	global.Db = nil

	requestContext, cancel := context.WithCancel(context.WithValue(context.Background(), requestContextTestKey{}, "detail"))
	cancel()
	var receivedContext context.Context
	loadPostDetailCache = func(ctx context.Context, _ string, _ func() (postResponse, error)) (postResponse, error) {
		receivedContext = ctx
		return postResponse{}, ctx.Err()
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/posts/42", nil).WithContext(requestContext)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	GetPostByID(ctx)

	if receivedContext != requestContext || receivedContext.Err() != context.Canceled {
		t.Fatal("detail loader did not receive the canceled request context")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote response body: %q", recorder.Body.String())
	}
}

func TestBookmarkMutationPassesCanceledRequestContextAndSuppressesResponse(t *testing.T) {
	restorePostBookmarkControllerMocks(t)
	requestContext, cancel := context.WithCancel(context.WithValue(context.Background(), requestContextTestKey{}, "bookmark"))
	cancel()

	var receivedContext context.Context
	mutatePostBookmark = func(ctx context.Context, _, _ uint, _ bool) (postBookmarkMutationResult, error) {
		receivedContext = ctx
		return postBookmarkMutationResult{}, ctx.Err()
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPut, "/api/posts/42/bookmark", nil).WithContext(requestContext)
	ginContext.Params = gin.Params{{Key: "id", Value: "42"}}
	ginContext.Set("user_id", uint(7))
	BookmarkPost(ginContext)

	if receivedContext != requestContext || receivedContext.Err() != context.Canceled {
		t.Fatal("bookmark mutation did not receive the canceled request context")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote response body: %q", recorder.Body.String())
	}
}
