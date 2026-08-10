package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newFollowControllerContext(method, targetID string, viewerID *uint, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		method,
		"/api/users/"+targetID+"/follow",
		bytes.NewBufferString(body),
	)
	ctx.Params = gin.Params{{Key: "id", Value: targetID}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func assertFollowStateResponse(t *testing.T, recorder *httptest.ResponseRecorder, expected userFollowStateResponse) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var actual userFollowStateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if actual != expected {
		t.Fatalf("response=%#v want %#v", actual, expected)
	}
}

func TestGetUserFollowStateReturnsCanonicalJSON(t *testing.T) {
	viewerID := uint(7)
	originalLoadUser := loadActiveFollowUser
	originalLoadState := loadFollowState
	t.Cleanup(func() {
		loadActiveFollowUser = originalLoadUser
		loadFollowState = originalLoadState
	})
	loadActiveFollowUser = func(id uint) error {
		if id != viewerID && id != 42 {
			t.Fatalf("unexpected user id=%d", id)
		}
		return nil
	}
	loadFollowState = func(viewer, target uint) (userFollowState, error) {
		if viewer != viewerID || target != 42 {
			t.Fatalf("unexpected state ids viewer=%d target=%d", viewer, target)
		}
		return userFollowState{Following: true, FollowerCount: 12, FollowingCount: 7}, nil
	}

	ctx, recorder := newFollowControllerContext(http.MethodGet, "42", &viewerID, "")
	GetUserFollowState(ctx)

	assertFollowStateResponse(t, recorder, userFollowStateResponse{
		UserID:         42,
		Following:      true,
		FollowerCount:  12,
		FollowingCount: 7,
	})
	if got := strings.TrimSpace(recorder.Body.String()); got != "{\"user_id\":42,\"following\":true,\"follower_count\":12,\"following_count\":7}" {
		t.Fatalf("canonical JSON=%s", got)
	}
}

func TestFollowUserUsesAuthenticatedViewerAndReturnsState(t *testing.T) {
	viewerID := uint(7)
	var gotViewerID, gotTargetID uint
	originalLoadUser := loadActiveFollowUser
	originalFollow := followAndLoadState
	t.Cleanup(func() {
		loadActiveFollowUser = originalLoadUser
		followAndLoadState = originalFollow
	})
	loadActiveFollowUser = func(uint) error { return nil }
	followAndLoadState = func(viewer, target uint) (userFollowState, error) {
		gotViewerID, gotTargetID = viewer, target
		return userFollowState{Following: true, FollowerCount: 1, FollowingCount: 3}, nil
	}

	ctx, recorder := newFollowControllerContext(
		http.MethodPut,
		"42",
		&viewerID,
		"{\"follower_id\":999,\"viewer_id\":999}",
	)
	FollowUser(ctx)

	assertFollowStateResponse(t, recorder, userFollowStateResponse{
		UserID:         42,
		Following:      true,
		FollowerCount:  1,
		FollowingCount: 3,
	})
	if gotViewerID != viewerID || gotTargetID != 42 {
		t.Fatalf("mutation ids viewer=%d target=%d", gotViewerID, gotTargetID)
	}
}

func TestUnfollowUserReturnsState(t *testing.T) {
	viewerID := uint(7)
	originalLoadUser := loadActiveFollowUser
	originalUnfollow := unfollowAndLoadState
	t.Cleanup(func() {
		loadActiveFollowUser = originalLoadUser
		unfollowAndLoadState = originalUnfollow
	})
	loadActiveFollowUser = func(uint) error { return nil }
	unfollowAndLoadState = func(viewer, target uint) (userFollowState, error) {
		if viewer != viewerID || target != 42 {
			t.Fatalf("unexpected mutation ids viewer=%d target=%d", viewer, target)
		}
		return userFollowState{Following: false, FollowerCount: 0, FollowingCount: 2}, nil
	}

	ctx, recorder := newFollowControllerContext(http.MethodDelete, "42", &viewerID, "")
	UnfollowUser(ctx)

	assertFollowStateResponse(t, recorder, userFollowStateResponse{
		UserID:         42,
		Following:      false,
		FollowerCount:  0,
		FollowingCount: 2,
	})
}

func TestFollowControllerRejectsInvalidTargetAndMissingAuth(t *testing.T) {
	viewerID := uint(7)
	for _, invalid := range []string{"0", "-1", "not-a-number"} {
		ctx, recorder := newFollowControllerContext(http.MethodGet, invalid, &viewerID, "")
		GetUserFollowState(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid target=%q status=%d body=%s", invalid, recorder.Code, recorder.Body.String())
		}
	}

	ctx, recorder := newFollowControllerContext(http.MethodGet, "42", nil, "")
	GetUserFollowState(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFollowControllerMapsMissingViewerAndTarget(t *testing.T) {
	viewerID := uint(7)
	originalLoadUser := loadActiveFollowUser
	t.Cleanup(func() { loadActiveFollowUser = originalLoadUser })

	loadActiveFollowUser = func(id uint) error {
		if id == viewerID {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	ctx, recorder := newFollowControllerContext(http.MethodPut, "42", &viewerID, "")
	FollowUser(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveFollowUser = func(id uint) error {
		if id == 42 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	ctx, recorder = newFollowControllerContext(http.MethodPut, "42", &viewerID, "")
	FollowUser(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing target status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFollowControllerRejectsSelfMutations(t *testing.T) {
	viewerID := uint(7)
	originalLoadUser := loadActiveFollowUser
	originalFollow := followAndLoadState
	originalUnfollow := unfollowAndLoadState
	t.Cleanup(func() {
		loadActiveFollowUser = originalLoadUser
		followAndLoadState = originalFollow
		unfollowAndLoadState = originalUnfollow
	})
	loadActiveFollowUser = func(uint) error { return nil }
	followAndLoadState = func(uint, uint) (userFollowState, error) {
		t.Fatal("self follow must not mutate")
		return userFollowState{}, nil
	}
	unfollowAndLoadState = func(uint, uint) (userFollowState, error) {
		t.Fatal("self unfollow must not mutate")
		return userFollowState{}, nil
	}

	ctx, recorder := newFollowControllerContext(http.MethodPut, "7", &viewerID, "")
	FollowUser(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("self follow status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newFollowControllerContext(http.MethodDelete, "7", &viewerID, "")
	UnfollowUser(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("self unfollow status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFollowControllerMapsDatastoreFailureToGeneric500(t *testing.T) {
	viewerID := uint(7)
	originalLoadUser := loadActiveFollowUser
	originalLoadState := loadFollowState
	t.Cleanup(func() {
		loadActiveFollowUser = originalLoadUser
		loadFollowState = originalLoadState
	})
	loadActiveFollowUser = func(uint) error { return nil }
	loadFollowState = func(uint, uint) (userFollowState, error) {
		return userFollowState{}, errors.New("database exploded")
	}

	ctx, recorder := newFollowControllerContext(http.MethodGet, "42", &viewerID, "")
	GetUserFollowState(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "database exploded") {
		t.Fatalf("raw datastore error leaked: %s", recorder.Body.String())
	}
}
