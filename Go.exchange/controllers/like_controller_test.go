package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/likes"

	"github.com/gin-gonic/gin"
)

func TestLikeArticleReturnsMutationResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	setArticleLikedState = func(uint, uint, bool) (articleLikeMutationResult, error) {
		return articleLikeMutationResult{Likes: 4, Liked: true, ChangedToLiked: true}, nil
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	LikeArticle(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikePayload(t, recorder)
	if payload["likes"] != float64(4) || payload["liked"] != true {
		t.Fatalf("payload=%v", payload)
	}
}

func TestLikeArticleReturnsUnavailableUntilRedisBaselineReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	setArticleLikedState = func(uint, uint, bool) (articleLikeMutationResult, error) {
		return articleLikeMutationResult{}, likes.ErrNotReady
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	LikeArticle(ctx)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetArticleLikesReturnsRedisState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeState = func(uint, uint) (articleLikeStateResult, error) {
		return articleLikeStateResult{Likes: 9, Liked: true}, nil
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	GetArticleLikes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikePayload(t, recorder)
	if payload["likes"] != float64(9) || payload["liked"] != true {
		t.Fatalf("payload=%v", payload)
	}
}

func TestWriteArticleLikeErrorReturnsInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeArticleLikeError(ctx, errors.New("redis unavailable"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func decodeLikePayload(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func restoreLikeControllerMocks(t *testing.T) {
	mutate, load := setArticleLikedState, loadArticleLikeState
	t.Cleanup(func() {
		setArticleLikedState = mutate
		loadArticleLikeState = load
	})
}
