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
)

func newRepostTestContext(method, path, body string, userID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	if userID != nil {
		ctx.Set("user_id", *userID)
	}
	if strings.HasPrefix(path, "/api/posts/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			ctx.Params = gin.Params{{Key: "id", Value: parts[2]}}
		}
	}
	return ctx, recorder
}

func restorePostRepostControllerMocks(t *testing.T) {
	loadState := loadPostRepostState
	mutate := mutatePostRepost
	loadMany := loadPostRepostStates
	t.Cleanup(func() {
		loadPostRepostState = loadState
		mutatePostRepost = mutate
		loadPostRepostStates = loadMany
	})
}

func decodeRepostPayload(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestGetPostRepostStateReturnsCurrentViewerState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restorePostRepostControllerMocks(t)
	loadPostRepostState = func(userID, postID uint) (postRepostStateResult, error) {
		if userID != 11 || postID != 42 {
			t.Fatalf("loader args user=%d article=%d", userID, postID)
		}
		return postRepostStateResult{Reposts: 12, Reposted: true}, nil
	}
	viewerID := uint(11)
	ctx, recorder := newRepostTestContext(http.MethodGet, "/api/posts/42/repost", "", &viewerID)
	GetPostRepostState(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeRepostPayload(t, recorder)
	if payload["reposts"] != float64(12) || payload["reposted"] != true {
		t.Fatalf("payload=%v", payload)
	}
}

func TestRepostMutationsReturnServerStateAndAreIdempotentAtHandlerBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restorePostRepostControllerMocks(t)
	var received []bool
	mutatePostRepost = func(userID, postID uint, reposted bool) (postRepostMutationResult, error) {
		if userID != 11 || postID != 42 {
			t.Fatalf("mutation args user=%d article=%d", userID, postID)
		}
		received = append(received, reposted)
		return postRepostMutationResult{Reposts: 8, Reposted: reposted}, nil
	}
	viewerID := uint(11)
	for _, testCase := range []struct {
		method  string
		handler gin.HandlerFunc
		want    bool
	}{
		{method: http.MethodPut, handler: RepostPost, want: true},
		{method: http.MethodDelete, handler: UndoRepostPost, want: false},
	} {
		ctx, recorder := newRepostTestContext(testCase.method, "/api/posts/42/repost", "", &viewerID)
		testCase.handler(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("method=%s status=%d body=%s", testCase.method, recorder.Code, recorder.Body.String())
		}
		payload := decodeRepostPayload(t, recorder)
		if payload["reposts"] != float64(8) || payload["reposted"] != testCase.want {
			t.Fatalf("method=%s payload=%v", testCase.method, payload)
		}
	}
	if len(received) != 2 || received[0] != true || received[1] != false {
		t.Fatalf("mutation states=%v", received)
	}
}

func TestPostRepostEndpointsRejectInvalidIDsAndMissingViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restorePostRepostControllerMocks(t)
	loadPostRepostState = func(uint, uint) (postRepostStateResult, error) {
		t.Fatal("state loader should not be called")
		return postRepostStateResult{}, nil
	}
	mutatePostRepost = func(uint, uint, bool) (postRepostMutationResult, error) {
		t.Fatal("mutation should not be called")
		return postRepostMutationResult{}, nil
	}
	for _, testCase := range []struct {
		method  string
		handler gin.HandlerFunc
		path    string
		viewer  *uint
		status  int
	}{
		{method: http.MethodGet, handler: GetPostRepostState, path: "/api/posts/0/repost", viewer: nil, status: http.StatusBadRequest},
		{method: http.MethodPut, handler: RepostPost, path: "/api/posts/42/repost", viewer: nil, status: http.StatusUnauthorized},
		{method: http.MethodDelete, handler: UndoRepostPost, path: "/api/posts/42/repost", viewer: nil, status: http.StatusUnauthorized},
		{method: http.MethodGet, handler: GetPostRepostState, path: "/api/posts/42/repost", viewer: nil, status: http.StatusUnauthorized},
	} {
		ctx, recorder := newRepostTestContext(testCase.method, testCase.path, "", testCase.viewer)
		testCase.handler(ctx)
		if recorder.Code != testCase.status {
			t.Fatalf("method=%s path=%s status=%d body=%s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPostRepostMapsUnavailableAndStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewerID := uint(11)
	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "unavailable", err: errPostRepostNotFound, want: http.StatusNotFound},
		{name: "store", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			restorePostRepostControllerMocks(t)
			loadPostRepostState = func(uint, uint) (postRepostStateResult, error) {
				return postRepostStateResult{}, testCase.err
			}
			ctx, recorder := newRepostTestContext(http.MethodGet, "/api/posts/42/repost", "", &viewerID)
			GetPostRepostState(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if testCase.want == http.StatusInternalServerError && strings.Contains(recorder.Body.String(), "database unavailable") {
				t.Fatalf("leaked store error: %s", recorder.Body.String())
			}
		})
	}
}

func TestGetPostRepostStatesDeduplicatesAndPreservesRequestOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restorePostRepostControllerMocks(t)
	loadPostRepostStates = func(userID uint, postIDs []uint) (postRepostStatesLoadResult, error) {
		if userID != 11 || !equalUintSlices(postIDs, []uint{7, 8, 9}) {
			t.Fatalf("loader args user=%d ids=%v", userID, postIDs)
		}
		return postRepostStatesLoadResult{
			States: map[uint]postRepostStateResult{
				7: {Reposts: 4, Reposted: true},
				9: {Reposts: 2, Reposted: false},
			},
			Unavailable: []uint{8},
		}, nil
	}
	viewerID := uint(11)
	ctx, recorder := newRepostTestContext(http.MethodPost, "/api/posts/repost-states", `{"post_ids":[7,7,8,9]}`, &viewerID)
	GetPostRepostStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload postRepostStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].PostID != 7 || payload.Items[0].Reposts != 4 || !payload.Items[0].Reposted || payload.Items[1].PostID != 9 {
		t.Fatalf("items=%+v", payload.Items)
	}
	if !equalUintSlices(payload.UnavailablePostIDs, []uint{8}) {
		t.Fatalf("unavailable=%v", payload.UnavailablePostIDs)
	}
}

func TestGetPostRepostStatesRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewerID := uint(11)
	rawIDs := make([]string, 101)
	for i := range rawIDs {
		rawIDs[i] = "7"
	}
	for _, body := range []string{
		`{"post_ids":[]}`,
		`{"post_ids":[0]}`,
		`{"post_ids":[-1]}`,
		`{"post_ids":["7"]}`,
		`{"post_ids":[7}`,
		`{}`,
		`{"post_ids":[` + strings.Join(rawIDs, ",") + `]}`,
	} {
		t.Run(body, func(t *testing.T) {
			restorePostRepostControllerMocks(t)
			loadPostRepostStates = func(uint, []uint) (postRepostStatesLoadResult, error) {
				t.Fatal("batch loader should not be called")
				return postRepostStatesLoadResult{}, nil
			}
			ctx, recorder := newRepostTestContext(http.MethodPost, "/api/posts/repost-states", body, &viewerID)
			GetPostRepostStates(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
			}
		})
	}
}
