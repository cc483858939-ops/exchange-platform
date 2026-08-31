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

func TestLikePostReturnsMutationResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	setPostLikedState = func(uint, uint, bool) (postLikeMutationResult, error) {
		return postLikeMutationResult{Likes: 4, Liked: true, ChangedToLiked: true}, nil
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	LikePost(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikePayload(t, recorder)
	if payload["likes"] != float64(4) || payload["liked"] != true {
		t.Fatalf("payload=%v", payload)
	}
}

func TestLikePostReturnsUnavailableUntilRedisBaselineReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	setPostLikedState = func(uint, uint, bool) (postLikeMutationResult, error) {
		return postLikeMutationResult{}, likes.ErrNotReady
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	LikePost(ctx)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostLikesReturnsRedisState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeState = func(uint, uint) (postLikeStateResult, error) {
		return postLikeStateResult{Likes: 9, Liked: true}, nil
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))
	GetPostLikes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikePayload(t, recorder)
	if payload["likes"] != float64(9) || payload["liked"] != true {
		t.Fatalf("payload=%v", payload)
	}
}

func TestGetPostLikeStatesReturnsFullyReadyItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeStates = func(userID uint, postIDs []uint) (postLikeStatesLoadResult, error) {
		if userID != 11 || !equalUintSlices(postIDs, []uint{7, 8}) {
			t.Fatalf("loader args user=%d ids=%v", userID, postIDs)
		}
		return postLikeStatesLoadResult{
			States: map[uint]postLikeStateResult{
				7: {Likes: 4, Liked: true},
				8: {Likes: 9, Liked: false},
			},
			Unavailable: []uint{},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7,8]}", uint(11))
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].PostID != 7 || !payload.Items[0].Liked || payload.Items[1].PostID != 8 || payload.Items[1].Likes != 9 || payload.Items[1].Liked {
		t.Fatalf("payload=%+v", payload)
	}
	if len(payload.UnavailablePostIDs) != 0 {
		t.Fatalf("unavailable=%v", payload.UnavailablePostIDs)
	}
}

func TestGetPostLikeStatesReturnsPartialAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeStates = func(uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{
			States: map[uint]postLikeStateResult{
				7: {Likes: 4, Liked: true},
				9: {Likes: 2, Liked: false},
			},
			Unavailable: []uint{8},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7,8,9]}", uint(11))
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].PostID != 7 || payload.Items[1].PostID != 9 {
		t.Fatalf("items=%+v", payload.Items)
	}
	if !equalUintSlices(payload.UnavailablePostIDs, []uint{8}) {
		t.Fatalf("unavailable=%v", payload.UnavailablePostIDs)
	}
}

func TestGetPostLikeStatesReturnsAllUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeStates = func(uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{
			States:      map[uint]postLikeStateResult{},
			Unavailable: []uint{7, 8},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7,8]}", uint(11))
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 0 || !equalUintSlices(payload.UnavailablePostIDs, []uint{7, 8}) {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestGetPostLikeStatesDeduplicatesBeforeLoading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	var loadedIDs []uint
	loadPostLikeStates = func(_ uint, postIDs []uint) (postLikeStatesLoadResult, error) {
		loadedIDs = append([]uint(nil), postIDs...)
		return postLikeStatesLoadResult{
			States: map[uint]postLikeStateResult{
				7: {Likes: 1, Liked: true},
				8: {Likes: 2, Liked: false},
			},
			Unavailable: []uint{},
		}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7,7,8,7]}", uint(11))
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if !equalUintSlices(loadedIDs, []uint{7, 8}) {
		t.Fatalf("loaded ids=%v", loadedIDs)
	}
	payload := decodeLikeStatesPayload(t, recorder)
	if len(payload.Items) != 2 || payload.Items[0].PostID != 7 || payload.Items[1].PostID != 8 {
		t.Fatalf("items=%+v", payload.Items)
	}
}

func TestGetPostLikeStatesRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawIDs := make([]string, 101)
	for i := range rawIDs {
		rawIDs[i] = "7"
	}
	overLimit := "{\"post_ids\":[" + strings.Join(rawIDs, ",") + "]}"
	cases := []struct {
		name string
		body string
	}{
		{name: "empty", body: "{\"post_ids\":[]}"},
		{name: "over raw limit", body: overLimit},
		{name: "zero", body: "{\"post_ids\":[0]}"},
		{name: "negative", body: "{\"post_ids\":[-1]}"},
		{name: "non numeric", body: "{\"post_ids\":[\"7\"]}"},
		{name: "malformed", body: "{\"post_ids\":[7}"},
		{name: "missing", body: "{}"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restoreLikeControllerMocks(t)
			loadPostLikeStates = func(uint, []uint) (postLikeStatesLoadResult, error) {
				t.Fatal("loader should not be called")
				return postLikeStatesLoadResult{}, nil
			}
			ctx, recorder := newLikeStatesContext(testCase.body, uint(11))
			GetPostLikeStates(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGetPostLikeStatesRequiresUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeStates = func(uint, []uint) (postLikeStatesLoadResult, error) {
		t.Fatal("loader should not be called")
		return postLikeStatesLoadResult{}, nil
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7]}", 0)
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostLikeStatesReturnsGlobalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)
	loadPostLikeStates = func(uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{}, errors.New("redis unavailable")
	}
	ctx, recorder := newLikeStatesContext("{\"post_ids\":[7]}", uint(11))
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWritePostLikeErrorReturnsInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writePostLikeError(ctx, errors.New("redis unavailable"))
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

func decodeLikeStatesPayload(t *testing.T, recorder *httptest.ResponseRecorder) postLikeStatesResponse {
	t.Helper()
	var payload postLikeStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func newLikeStatesContext(body string, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts/like-states", bytes.NewBufferString(body))
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
	mutate, load, loadMany := setPostLikedState, loadPostLikeState, loadPostLikeStates
	t.Cleanup(func() {
		setPostLikedState = mutate
		loadPostLikeState = load
		loadPostLikeStates = loadMany
	})
}
