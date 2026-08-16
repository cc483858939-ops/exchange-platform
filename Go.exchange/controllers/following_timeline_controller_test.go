package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newFollowingTimelineTestContext(path string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func TestGetFollowingTimelineReturnsCanonicalResponse(t *testing.T) {
	viewerID := uint(7)
	nextCursor := "opaque-cursor"
	originalActive := loadActiveFollowingViewer
	originalLoader := loadFollowingTimelinePage
	t.Cleanup(func() {
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
	})

	loadActiveFollowingViewer = func(id uint) error {
		if id != viewerID {
			t.Fatalf("active viewer id=%d", id)
		}
		return nil
	}
	loadFollowingTimelinePage = func(id uint, limit int, cursor *articleCursor) (articlePageResponse, error) {
		if id != viewerID || limit != 20 || cursor != nil {
			t.Fatalf("loader args id=%d limit=%d cursor=%v", id, limit, cursor)
		}
		return articlePageResponse{
			Items:      []articleResponse{{ID: 101, Content: "Canonical following body", Author: publicAuthorResponse{ID: 9, Username: "alice"}}},
			NextCursor: &nextCursor,
		}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response articlePageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ID != 101 || response.Items[0].Content != "Canonical following body" || response.Items[0].Author.Username != "alice" {
		t.Fatalf("response=%#v", response)
	}
	if response.NextCursor == nil || *response.NextCursor != nextCursor {
		t.Fatalf("next cursor=%v", response.NextCursor)
	}
}

func TestFollowingTimelineDefaultsAndClampsLimit(t *testing.T) {
	viewerID := uint(7)
	originalActive := loadActiveFollowingViewer
	originalLoader := loadFollowingTimelinePage
	t.Cleanup(func() {
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
	})
	loadActiveFollowingViewer = func(uint) error { return nil }
	var limits []int
	loadFollowingTimelinePage = func(_ uint, limit int, _ *articleCursor) (articlePageResponse, error) {
		limits = append(limits, limit)
		return articlePageResponse{Items: []articleResponse{}}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("default response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newFollowingTimelineTestContext("/api/feed/following?limit=100", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clamped status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(limits) != 2 || limits[0] != defaultArticleLimit || limits[1] != maxArticleLimit {
		t.Fatalf("limits=%v", limits)
	}
}

func TestFollowingTimelineRejectsInvalidLimit(t *testing.T) {
	viewerID := uint(7)
	originalActive := loadActiveFollowingViewer
	originalLoader := loadFollowingTimelinePage
	t.Cleanup(func() {
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
	})
	loadActiveFollowingViewer = func(uint) error { return nil }
	loadFollowingTimelinePage = func(uint, int, *articleCursor) (articlePageResponse, error) {
		t.Fatal("timeline loader should not be called")
		return articlePageResponse{}, nil
	}

	for _, raw := range []string{"0", "-1", "not-a-number"} {
		ctx, recorder := newFollowingTimelineTestContext("/api/feed/following?limit="+raw, &viewerID)
		GetFollowingTimeline(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
}

func TestFollowingTimelineAcceptsValidCursor(t *testing.T) {
	viewerID := uint(7)
	originalActive := loadActiveFollowingViewer
	originalLoader := loadFollowingTimelinePage
	t.Cleanup(func() {
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
	})
	loadActiveFollowingViewer = func(uint) error { return nil }
	want := articleCursor{Version: articleCursorVersion, PublishedAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC), ID: 42}
	raw, err := encodeArticleCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	var received *articleCursor
	loadFollowingTimelinePage = func(_ uint, _ int, cursor *articleCursor) (articlePageResponse, error) {
		received = cursor
		return articlePageResponse{Items: []articleResponse{}}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following?cursor="+raw, &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if received == nil || !received.PublishedAt.Equal(want.PublishedAt) || received.ID != want.ID {
		t.Fatalf("received cursor=%v", received)
	}
}

func TestFollowingTimelineRejectsInvalidCursor(t *testing.T) {
	zeroTime, err := json.Marshal(articleCursor{Version: articleCursorVersion, ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	zeroID, err := json.Marshal(articleCursor{Version: articleCursorVersion, PublishedAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	badJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))
	for _, raw := range []string{
		"",
		"not-base64",
		badJSON,
		base64.RawURLEncoding.EncodeToString(zeroTime),
		base64.RawURLEncoding.EncodeToString(zeroID),
	} {
		viewerID := uint(7)
		originalActive := loadActiveFollowingViewer
		originalLoader := loadFollowingTimelinePage
		loadActiveFollowingViewer = func(uint) error { return nil }
		loadFollowingTimelinePage = func(uint, int, *articleCursor) (articlePageResponse, error) {
			t.Fatal("timeline loader should not be called")
			return articlePageResponse{}, nil
		}
		ctx, recorder := newFollowingTimelineTestContext("/api/feed/following?cursor="+raw, &viewerID)
		GetFollowingTimeline(ctx)
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
		if recorder.Code != http.StatusBadRequest || strings.TrimSpace(recorder.Body.String()) != `{"error":"invalid cursor"}` {
			t.Fatalf("cursor=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
}

func TestFollowingTimelineHandlesMissingInactiveAndFailedViewer(t *testing.T) {
	originalActive := loadActiveFollowingViewer
	originalLoader := loadFollowingTimelinePage
	t.Cleanup(func() {
		loadActiveFollowingViewer = originalActive
		loadFollowingTimelinePage = originalLoader
	})
	loadFollowingTimelinePage = func(uint, int, *articleCursor) (articlePageResponse, error) {
		t.Fatal("timeline loader should not be called")
		return articlePageResponse{}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following", nil)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	viewerID := uint(7)
	loadActiveFollowingViewer = func(uint) error { return gorm.ErrRecordNotFound }
	ctx, recorder = newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveFollowingViewer = func(uint) error { return errors.New("database unavailable") }
	ctx, recorder = newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "database unavailable") {
		t.Fatalf("viewer datastore failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveFollowingViewer = func(uint) error { return nil }
	loadFollowingTimelinePage = func(uint, int, *articleCursor) (articlePageResponse, error) {
		return articlePageResponse{}, errors.New("query failed")
	}
	ctx, recorder = newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "query failed") {
		t.Fatalf("timeline datastore failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBuildFollowingTimelineResponseUsesLastReturnedItemForCursor(t *testing.T) {
	firstTime := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(-time.Minute)
	articles := []articleResponse{
		{ID: 10, PublishedAt: &firstTime},
		{ID: 9, PublishedAt: &secondTime},
		{ID: 8, PublishedAt: &secondTime},
	}
	response, err := buildArticlePageResponse(articles, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].ID != 10 || response.Items[1].ID != 9 || response.NextCursor == nil {
		t.Fatalf("response=%#v", response)
	}
	cursor, err := decodeArticleCursor(*response.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ID != 9 || !cursor.PublishedAt.Equal(secondTime) {
		t.Fatalf("cursor=%#v", cursor)
	}

	empty, err := buildArticlePageResponse(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty=%#v", empty)
	}
}

func TestFollowingTimelineCursorRoundTripAndValidation(t *testing.T) {
	want := articleCursor{Version: articleCursorVersion, PublishedAt: time.Date(2026, 8, 10, 14, 0, 0, 123456000, time.UTC), ID: 42}
	raw, err := encodeArticleCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeArticleCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PublishedAt.Equal(want.PublishedAt) || got.ID != want.ID {
		t.Fatalf("got=%#v want=%#v", got, want)
	}

	if _, err := encodeArticleCursor(articleCursor{}); err == nil {
		t.Fatal("expected zero cursor encoding error")
	}
}
