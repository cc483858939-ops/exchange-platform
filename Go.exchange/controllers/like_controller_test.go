package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGetArticleLikeStatesReturnsFullyReadyItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeStates = func(userID uint, articleIDs []uint) (articleLikeStatesLoadResult, error) {
		if userID != 11 || !equalUintSlices(articleIDs, []uint{7, 8}) {
			t.Fatalf("loader args user=%d ids=%v", userID, articleIDs)
		}
		return articleLikeStatesLoadResult{
			States: map[uint]articleLikeStateResult{
				7: {Likes: 4, Liked: true},
				8: {Likes: 9, Liked: false},
			},
			Unavailable: []uint{},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7,8]}", uint(11))
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].ArticleID != 7 || !payload.Items[0].Liked || payload.Items[1].ArticleID != 8 || payload.Items[1].Likes != 9 || payload.Items[1].Liked {
		t.Fatalf("payload=%+v", payload)
	}
	if len(payload.UnavailableArticleIDs) != 0 {
		t.Fatalf("unavailable=%v", payload.UnavailableArticleIDs)
	}
}

func TestGetArticleLikeStatesReturnsPartialAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeStates = func(uint, []uint) (articleLikeStatesLoadResult, error) {
		return articleLikeStatesLoadResult{
			States: map[uint]articleLikeStateResult{
				7: {Likes: 4, Liked: true},
				9: {Likes: 2, Liked: false},
			},
			Unavailable: []uint{8},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7,8,9]}", uint(11))
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].ArticleID != 7 || payload.Items[1].ArticleID != 9 {
		t.Fatalf("items=%+v", payload.Items)
	}
	if !equalUintSlices(payload.UnavailableArticleIDs, []uint{8}) {
		t.Fatalf("unavailable=%v", payload.UnavailableArticleIDs)
	}
}

func TestGetArticleLikeStatesReturnsAllUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeStates = func(uint, []uint) (articleLikeStatesLoadResult, error) {
		return articleLikeStatesLoadResult{
			States:      map[uint]articleLikeStateResult{},
			Unavailable: []uint{7, 8},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7,8]}", uint(11))
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 0 || !equalUintSlices(payload.UnavailableArticleIDs, []uint{7, 8}) {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestGetArticleLikeStatesDeduplicatesBeforeLoading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	var loadedIDs []uint
	loadArticleLikeStates = func(_ uint, articleIDs []uint) (articleLikeStatesLoadResult, error) {
		loadedIDs = append([]uint(nil), articleIDs...)
		return articleLikeStatesLoadResult{
			States: map[uint]articleLikeStateResult{
				7: {Likes: 1, Liked: true},
				8: {Likes: 2, Liked: false},
			},
			Unavailable: []uint{},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7,7,8,7]}", uint(11))
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if !equalUintSlices(loadedIDs, []uint{7, 8}) {
		t.Fatalf("loaded ids=%v", loadedIDs)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].ArticleID != 7 || payload.Items[1].ArticleID != 8 {
		t.Fatalf("items=%+v", payload.Items)
	}
}

func TestGetArticleLikeStatesRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawIDs := make([]string, 101)
	for i := range rawIDs {
		rawIDs[i] = "7"
	}
	overLimit := "{\"article_ids\":[" + strings.Join(rawIDs, ",") + "]}"
	cases := []struct {
		name string
		body string
	}{
		{name: "empty", body: "{\"article_ids\":[]}"},
		{name: "over raw limit", body: overLimit},
		{name: "zero", body: "{\"article_ids\":[0]}"},
		{name: "negative", body: "{\"article_ids\":[-1]}"},
		{name: "non numeric", body: "{\"article_ids\":[\"7\"]}"},
		{name: "malformed", body: "{\"article_ids\":[7}"},
		{name: "missing", body: "{}"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restoreLikeControllerMocks(t)
			loadArticleLikeStates = func(uint, []uint) (articleLikeStatesLoadResult, error) {
				t.Fatal("loader should not be called")
				return articleLikeStatesLoadResult{}, nil
			}
			ctx, recorder := newLikeStatesContext(testCase.body, uint(11))
			GetArticleLikeStates(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGetArticleLikeStatesRequiresUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeStates = func(uint, []uint) (articleLikeStatesLoadResult, error) {
		t.Fatal("loader should not be called")
		return articleLikeStatesLoadResult{}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7]}", 0)
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetArticleLikeStatesReturnsGlobalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadArticleLikeStates = func(uint, []uint) (articleLikeStatesLoadResult, error) {
		return articleLikeStatesLoadResult{}, errors.New("redis unavailable")
	}
	ctx, recorder := newLikeStatesContext("{\"article_ids\":[7]}", uint(11))
	GetArticleLikeStates(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
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

func decodeLikeStatesPayload(t *testing.T, recorder *httptest.ResponseRecorder) articleLikeStatesResponse {
	t.Helper()
	var payload articleLikeStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func newLikeStatesContext(body string, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles/like-states", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		ctx.Set("user_id", userID)
	}
	return ctx, recorder
}

func equalUintSlices(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func restoreLikeControllerMocks(t *testing.T) {
	mutate, load, loadMany := setArticleLikedState, loadArticleLikeState, loadArticleLikeStates
	t.Cleanup(func() {
		setArticleLikedState = mutate
		loadArticleLikeState = load
		loadArticleLikeStates = loadMany
	})
}
