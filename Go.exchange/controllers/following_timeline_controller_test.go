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

func TestGetFollowingTimelineReturnsActivityResponse(t *testing.T) {
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
	loadFollowingTimelinePage = func(id uint, limit int, cursor *followingCursor) (followingTimelinePageResponse, error) {
		if id != viewerID || limit != 20 || cursor != nil {
			t.Fatalf("loader args id=%d limit=%d cursor=%v", id, limit, cursor)
		}
		return followingTimelinePageResponse{
			Items: []followingTimelineItem{{
				ActivityType: followingActivityRepost,
				ActivityAt:   time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC),
				SourceID:     202,
				Actor:        publicAuthorResponse{ID: 11, Username: "alice"},
				Article:      articleResponse{ID: 101, Content: "Canonical following body", Author: publicAuthorResponse{ID: 9, Username: "bob"}},
			}},
			NextCursor: &nextCursor,
		}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following", &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response followingTimelinePageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Article.ID != 101 || response.Items[0].ActivityType != followingActivityRepost || response.Items[0].Article.Content != "Canonical following body" || response.Items[0].Article.Author.Username != "bob" || response.Items[0].Actor.Username != "alice" {
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
	loadFollowingTimelinePage = func(_ uint, limit int, _ *followingCursor) (followingTimelinePageResponse, error) {
		limits = append(limits, limit)
		return followingTimelinePageResponse{Items: []followingTimelineItem{}}, nil
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
	if len(limits) != 2 || limits[0] != defaultFollowingLimit || limits[1] != maxFollowingLimit {
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
	loadFollowingTimelinePage = func(uint, int, *followingCursor) (followingTimelinePageResponse, error) {
		t.Fatal("timeline loader should not be called")
		return followingTimelinePageResponse{}, nil
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
	want := followingCursor{ActivityAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC), ActivityType: string(followingActivityRepost), SourceID: 42}
	raw, err := encodeFollowingCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	var received *followingCursor
	loadFollowingTimelinePage = func(_ uint, _ int, cursor *followingCursor) (followingTimelinePageResponse, error) {
		received = cursor
		return followingTimelinePageResponse{Items: []followingTimelineItem{}}, nil
	}

	ctx, recorder := newFollowingTimelineTestContext("/api/feed/following?cursor="+raw, &viewerID)
	GetFollowingTimeline(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if received == nil || !received.ActivityAt.Equal(want.ActivityAt) || received.ActivityType != want.ActivityType || received.SourceID != want.SourceID {
		t.Fatalf("received cursor=%v", received)
	}
}

func TestFollowingTimelineRejectsInvalidCursor(t *testing.T) {
	zeroTime, err := json.Marshal(followingCursor{ActivityType: string(followingActivityPost), SourceID: 1})
	if err != nil {
		t.Fatal(err)
	}
	zeroID, err := json.Marshal(followingCursor{ActivityAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC), ActivityType: string(followingActivityPost)})
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
		loadFollowingTimelinePage = func(uint, int, *followingCursor) (followingTimelinePageResponse, error) {
			t.Fatal("timeline loader should not be called")
			return followingTimelinePageResponse{}, nil
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
	loadFollowingTimelinePage = func(uint, int, *followingCursor) (followingTimelinePageResponse, error) {
		t.Fatal("timeline loader should not be called")
		return followingTimelinePageResponse{}, nil
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
	loadFollowingTimelinePage = func(uint, int, *followingCursor) (followingTimelinePageResponse, error) {
		return followingTimelinePageResponse{}, errors.New("query failed")
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
	rows := []followingActivityQueryRow{
		{ActivityType: "post", ActivityAt: firstTime, SourceID: 10, ArticleID: 10, ActorID: 1, ActivityRank: 1},
		{ActivityType: "repost", ActivityAt: secondTime, SourceID: 9, ArticleID: 9, ActorID: 2, ActivityRank: 2},
		{ActivityType: "post", ActivityAt: secondTime, SourceID: 8, ArticleID: 8, ActorID: 3, ActivityRank: 1},
	}
	articles := map[uint]articleResponse{
		10: {ID: 10},
		9:  {ID: 9},
		8:  {ID: 8},
	}
	actors := map[uint]publicAuthorResponse{
		1: {ID: 1},
		2: {ID: 2},
		3: {ID: 3},
	}
	response, err := buildFollowingTimelinePageResponse(rows, articles, actors, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Article.ID != 10 || response.Items[1].Article.ID != 9 || response.NextCursor == nil {
		t.Fatalf("response=%#v", response)
	}
	cursor, err := decodeFollowingCursor(*response.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.SourceID != 9 || cursor.ActivityType != "repost" || !cursor.ActivityAt.Equal(secondTime) {
		t.Fatalf("cursor=%#v", cursor)
	}

	empty, err := buildFollowingTimelinePageResponse(nil, articles, actors, 2)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty=%#v", empty)
	}
}

func TestFollowingTimelineCursorRoundTripAndValidation(t *testing.T) {
	want := followingCursor{ActivityAt: time.Date(2026, 8, 10, 14, 0, 0, 123456000, time.UTC), ActivityType: "repost", SourceID: 42}
	raw, err := encodeFollowingCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeFollowingCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ActivityAt.Equal(want.ActivityAt) || got.ActivityType != want.ActivityType || got.SourceID != want.SourceID {
		t.Fatalf("got=%#v want=%#v", got, want)
	}

	if _, err := encodeFollowingCursor(followingCursor{}); err == nil {
		t.Fatal("expected zero cursor encoding error")
	}
}
