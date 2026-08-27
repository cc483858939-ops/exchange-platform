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
	if strings.HasPrefix(path, "/api/articles/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			ctx.Params = gin.Params{{Key: "id", Value: parts[2]}}
		}
	}
	return ctx, recorder
}

func restoreArticleRepostControllerMocks(t *testing.T) {
	loadState := loadArticleRepostState
	mutate := mutateArticleRepost
	loadMany := loadArticleRepostStates
	t.Cleanup(func() {
		loadArticleRepostState = loadState
		mutateArticleRepost = mutate
		loadArticleRepostStates = loadMany
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

func TestGetArticleRepostStateReturnsCurrentViewerState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreArticleRepostControllerMocks(t)
	loadArticleRepostState = func(userID, articleID uint) (articleRepostStateResult, error) {
		if userID != 11 || articleID != 42 {
			t.Fatalf("loader args user=%d article=%d", userID, articleID)
		}
		return articleRepostStateResult{Reposts: 12, Reposted: true}, nil
	}
	viewerID := uint(11)
	ctx, recorder := newRepostTestContext(http.MethodGet, "/api/articles/42/repost", "", &viewerID)
	GetArticleRepostState(ctx)
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
	restoreArticleRepostControllerMocks(t)
	var received []bool
	mutateArticleRepost = func(userID, articleID uint, reposted bool) (articleRepostMutationResult, error) {
		if userID != 11 || articleID != 42 {
			t.Fatalf("mutation args user=%d article=%d", userID, articleID)
		}
		received = append(received, reposted)
		return articleRepostMutationResult{Reposts: 8, Reposted: reposted}, nil
	}
	viewerID := uint(11)
	for _, testCase := range []struct {
		method  string
		handler gin.HandlerFunc
		want    bool
	}{
		{method: http.MethodPut, handler: RepostArticle, want: true},
		{method: http.MethodDelete, handler: UndoRepostArticle, want: false},
	} {
		ctx, recorder := newRepostTestContext(testCase.method, "/api/articles/42/repost", "", &viewerID)
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

func TestArticleRepostEndpointsRejectInvalidIDsAndMissingViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreArticleRepostControllerMocks(t)
	loadArticleRepostState = func(uint, uint) (articleRepostStateResult, error) {
		t.Fatal("state loader should not be called")
		return articleRepostStateResult{}, nil
	}
	mutateArticleRepost = func(uint, uint, bool) (articleRepostMutationResult, error) {
		t.Fatal("mutation should not be called")
		return articleRepostMutationResult{}, nil
	}
	for _, testCase := range []struct {
		method  string
		handler gin.HandlerFunc
		path    string
		viewer  *uint
		status  int
	}{
		{method: http.MethodGet, handler: GetArticleRepostState, path: "/api/articles/0/repost", viewer: nil, status: http.StatusBadRequest},
		{method: http.MethodPut, handler: RepostArticle, path: "/api/articles/42/repost", viewer: nil, status: http.StatusUnauthorized},
		{method: http.MethodDelete, handler: UndoRepostArticle, path: "/api/articles/42/repost", viewer: nil, status: http.StatusUnauthorized},
		{method: http.MethodGet, handler: GetArticleRepostState, path: "/api/articles/42/repost", viewer: nil, status: http.StatusUnauthorized},
	} {
		ctx, recorder := newRepostTestContext(testCase.method, testCase.path, "", testCase.viewer)
		testCase.handler(ctx)
		if recorder.Code != testCase.status {
			t.Fatalf("method=%s path=%s status=%d body=%s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestArticleRepostMapsUnavailableAndStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewerID := uint(11)
	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "unavailable", err: errArticleRepostNotFound, want: http.StatusNotFound},
		{name: "store", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			restoreArticleRepostControllerMocks(t)
			loadArticleRepostState = func(uint, uint) (articleRepostStateResult, error) {
				return articleRepostStateResult{}, testCase.err
			}
			ctx, recorder := newRepostTestContext(http.MethodGet, "/api/articles/42/repost", "", &viewerID)
			GetArticleRepostState(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if testCase.want == http.StatusInternalServerError && strings.Contains(recorder.Body.String(), "database unavailable") {
				t.Fatalf("leaked store error: %s", recorder.Body.String())
			}
		})
	}
}

func TestGetArticleRepostStatesDeduplicatesAndPreservesRequestOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreArticleRepostControllerMocks(t)
	loadArticleRepostStates = func(userID uint, articleIDs []uint) (articleRepostStatesLoadResult, error) {
		if userID != 11 || !equalUintSlices(articleIDs, []uint{7, 8, 9}) {
			t.Fatalf("loader args user=%d ids=%v", userID, articleIDs)
		}
		return articleRepostStatesLoadResult{
			States: map[uint]articleRepostStateResult{
				7: {Reposts: 4, Reposted: true},
				9: {Reposts: 2, Reposted: false},
			},
			Unavailable: []uint{8},
		}, nil
	}
	viewerID := uint(11)
	ctx, recorder := newRepostTestContext(http.MethodPost, "/api/articles/repost-states", `{"article_ids":[7,7,8,9]}`, &viewerID)
	GetArticleRepostStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload articleRepostStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].ArticleID != 7 || payload.Items[0].Reposts != 4 || !payload.Items[0].Reposted || payload.Items[1].ArticleID != 9 {
		t.Fatalf("items=%+v", payload.Items)
	}
	if !equalUintSlices(payload.UnavailableArticleIDs, []uint{8}) {
		t.Fatalf("unavailable=%v", payload.UnavailableArticleIDs)
	}
}

func TestGetArticleRepostStatesRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewerID := uint(11)
	rawIDs := make([]string, 101)
	for i := range rawIDs {
		rawIDs[i] = "7"
	}
	for _, body := range []string{
		`{"article_ids":[]}`,
		`{"article_ids":[0]}`,
		`{"article_ids":[-1]}`,
		`{"article_ids":["7"]}`,
		`{"article_ids":[7}`,
		`{}`,
		`{"article_ids":[` + strings.Join(rawIDs, ",") + `]}`,
	} {
		t.Run(body, func(t *testing.T) {
			restoreArticleRepostControllerMocks(t)
			loadArticleRepostStates = func(uint, []uint) (articleRepostStatesLoadResult, error) {
				t.Fatal("batch loader should not be called")
				return articleRepostStatesLoadResult{}, nil
			}
			ctx, recorder := newRepostTestContext(http.MethodPost, "/api/articles/repost-states", body, &viewerID)
			GetArticleRepostStates(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
			}
		})
	}
}
