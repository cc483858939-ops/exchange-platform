package controllers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newPostBookmarkStateTestContext(body string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts/bookmark-states", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}

func newPostBookmarkMutationTestContext(method, path string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}

func restorePostBookmarkControllerMocks(t *testing.T) {
	t.Helper()
	loadStates := loadPostBookmarkStates
	mutate := mutatePostBookmark
	loadHistory := loadPostBookmarkHistoryPage
	t.Cleanup(func() {
		loadPostBookmarkStates = loadStates
		mutatePostBookmark = mutate
		loadPostBookmarkHistoryPage = loadHistory
	})
}

func TestGetPostBookmarkStatesDeduplicatesAndPreservesRequestOrder(t *testing.T) {
	viewerID := uint(17)
	restorePostBookmarkControllerMocks(t)
	loadPostBookmarkStates = func(id uint, postIDs []uint) (postBookmarkStatesLoadResult, error) {
		if id != viewerID {
			t.Fatalf("viewer id=%d", id)
		}
		want := []uint{9, 4, 7}
		if len(postIDs) != len(want) {
			t.Fatalf("post ids=%v", postIDs)
		}
		for index := range want {
			if postIDs[index] != want[index] {
				t.Fatalf("post ids=%v want=%v", postIDs, want)
			}
		}
		return postBookmarkStatesLoadResult{
			States: map[uint]postBookmarkStateResult{
				9: {PostID: 9, Bookmarked: true},
				7: {PostID: 7, Bookmarked: false},
			},
			Unavailable: []uint{4},
		}, nil
	}

	ctx, recorder := newPostBookmarkStateTestContext(`{"post_ids":[9,4,9,7]}`, viewerID)
	GetPostBookmarkStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != `{"items":[{"post_id":9,"bookmarked":true},{"post_id":7,"bookmarked":false}],"unavailable_post_ids":[4]}` {
		t.Fatalf("body=%s", got)
	}
}

func TestGetPostBookmarkStatesRejectsInvalidRequests(t *testing.T) {
	restorePostBookmarkControllerMocks(t)
	loadPostBookmarkStates = func(uint, []uint) (postBookmarkStatesLoadResult, error) {
		t.Fatal("bookmark state loader should not be called")
		return postBookmarkStatesLoadResult{}, nil
	}
	for _, body := range []string{`{"post_ids":[]}`, `{"post_ids":[0]}`, `{`} {
		ctx, recorder := newPostBookmarkStateTestContext(body, uint(17))
		GetPostBookmarkStates(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	tooMany := make([]uint, maxPostBookmarkStateIDs+1)
	for index := range tooMany {
		tooMany[index] = uint(index + 1)
	}
	encoded, err := json.Marshal(map[string]any{"post_ids": tooMany})
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newPostBookmarkStateTestContext(string(encoded), uint(17))
	GetPostBookmarkStates(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("too many status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newPostBookmarkStateTestContext(`{"post_ids":[1]}`, nil)
	GetPostBookmarkStates(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPostBookmarkMutationsAreIdempotentAtTheHandlerBoundary(t *testing.T) {
	viewerID := uint(17)
	restorePostBookmarkControllerMocks(t)
	var calls []bool
	mutatePostBookmark = func(userID, postID uint, bookmarked bool) (postBookmarkMutationResult, error) {
		if userID != viewerID || postID != 42 {
			t.Fatalf("mutation args user=%d post=%d", userID, postID)
		}
		calls = append(calls, bookmarked)
		return postBookmarkMutationResult{PostID: postID, Bookmarked: bookmarked}, nil
	}

	ctx, recorder := newPostBookmarkMutationTestContext(http.MethodPut, "/api/posts/42/bookmark", viewerID)
	BookmarkPost(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"post_id":42,"bookmarked":true}` {
		t.Fatalf("bookmark status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newPostBookmarkMutationTestContext(http.MethodDelete, "/api/posts/42/bookmark", viewerID)
	UnbookmarkPost(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"post_id":42,"bookmarked":false}` {
		t.Fatalf("unbookmark status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Fatalf("mutation calls=%v", calls)
	}
}

func TestPostBookmarkMutationErrorsAndInvalidIDs(t *testing.T) {
	restorePostBookmarkControllerMocks(t)
	mutatePostBookmark = func(uint, uint, bool) (postBookmarkMutationResult, error) {
		return postBookmarkMutationResult{}, errPostBookmarkUnavailable
	}
	ctx, recorder := newPostBookmarkMutationTestContext(http.MethodPut, "/api/posts/42/bookmark", uint(17))
	BookmarkPost(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unavailable status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	mutatePostBookmark = func(uint, uint, bool) (postBookmarkMutationResult, error) {
		return postBookmarkMutationResult{}, errors.New("storage unavailable")
	}
	ctx, recorder = newPostBookmarkMutationTestContext(http.MethodPut, "/api/posts/42/bookmark", uint(17))
	BookmarkPost(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "storage unavailable") {
		t.Fatalf("storage status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newPostBookmarkMutationTestContext(http.MethodPut, "/api/posts/0/bookmark", uint(17))
	ctx.Params = gin.Params{{Key: "id", Value: "0"}}
	BookmarkPost(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetMyBookmarksCanonicalResponseAndLimits(t *testing.T) {
	viewerID := uint(17)
	restorePostBookmarkControllerMocks(t)
	originalActive := loadActiveProfileViewer
	t.Cleanup(func() { loadActiveProfileViewer = originalActive })
	loadActiveProfileViewer = func(id uint) (models.User, error) {
		if id != viewerID {
			t.Fatalf("active viewer id=%d", id)
		}
		return models.User{}, nil
	}
	var limits []int
	loadPostBookmarkHistoryPage = func(id uint, limit int, cursor *bookmarkHistoryCursor) (postPageResponse, error) {
		if id != viewerID || cursor != nil {
			t.Fatalf("loader args id=%d limit=%d cursor=%v", id, limit, cursor)
		}
		limits = append(limits, limit)
		return postPageResponse{Items: []postResponse{}}, nil
	}

	ctx, recorder := newLikedHistoryTestContext("/api/me/bookmarks", viewerID)
	GetMyBookmarks(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("default status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newLikedHistoryTestContext("/api/me/bookmarks?limit=100", viewerID)
	GetMyBookmarks(ctx)
	if recorder.Code != http.StatusOK || len(limits) != 2 || limits[0] != defaultBookmarkHistoryLimit || limits[1] != maxBookmarkHistoryLimit {
		t.Fatalf("clamped status=%d limits=%v body=%s", recorder.Code, limits, recorder.Body.String())
	}
}

func TestBookmarkHistoryCursorRoundTripAndValidation(t *testing.T) {
	want := bookmarkHistoryCursor{
		Version:      bookmarkHistoryCursorVersion,
		BookmarkedAt: time.Date(2026, 8, 10, 14, 0, 0, 123456000, time.UTC),
		PostID:       42,
	}
	raw, err := encodeBookmarkHistoryCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeBookmarkHistoryCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.BookmarkedAt.Equal(want.BookmarkedAt) || got.PostID != want.PostID || got.Version != want.Version {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
	zeroTime, err := json.Marshal(bookmarkHistoryCursor{Version: bookmarkHistoryCursorVersion, PostID: 1})
	if err != nil {
		t.Fatal(err)
	}
	badJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))
	for _, raw := range []string{"", "not-base64", badJSON, base64.RawURLEncoding.EncodeToString(zeroTime)} {
		if _, err := decodeBookmarkHistoryCursor(raw); err == nil {
			t.Fatalf("cursor %q unexpectedly decoded", raw)
		}
	}
}

func TestGetMyBookmarksAuthenticationAndLoaderErrors(t *testing.T) {
	restorePostBookmarkControllerMocks(t)
	originalActive := loadActiveProfileViewer
	t.Cleanup(func() { loadActiveProfileViewer = originalActive })
	loadPostBookmarkHistoryPage = func(uint, int, *bookmarkHistoryCursor) (postPageResponse, error) {
		t.Fatal("bookmark history loader should not be called")
		return postPageResponse{}, nil
	}
	ctx, recorder := newLikedHistoryTestContext("/api/me/bookmarks", nil)
	GetMyBookmarks(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	viewerID := uint(17)
	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, gorm.ErrRecordNotFound }
	ctx, recorder = newLikedHistoryTestContext("/api/me/bookmarks", viewerID)
	GetMyBookmarks(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(id uint) (models.User, error) { return models.User{Model: gorm.Model{ID: id}}, nil }
	loadPostBookmarkHistoryPage = func(uint, int, *bookmarkHistoryCursor) (postPageResponse, error) {
		return postPageResponse{}, errors.New("query failed")
	}
	ctx, recorder = newLikedHistoryTestContext("/api/me/bookmarks", viewerID)
	GetMyBookmarks(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "query failed") {
		t.Fatalf("loader failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
