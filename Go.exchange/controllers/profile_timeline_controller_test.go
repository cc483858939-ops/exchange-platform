package controllers

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGetUserTimelineUsesGenericPaginationAndActiveProfileLookup(t *testing.T) {
	originalProfileLoader := loadUserTimelineProfile
	originalTimelineLoader := loadUserTimelinePage
	t.Cleanup(func() {
		loadUserTimelineProfile = originalProfileLoader
		loadUserTimelinePage = originalTimelineLoader
	})

	const userID = uint(7)
	loadUserTimelineProfile = func(id uint) (publicUserResponse, error) {
		if id != userID {
			t.Fatalf("profile id=%d", id)
		}
		return publicUserResponse{ID: id}, nil
	}
	wantCursor := timelineCursor{
		ActivityAt:   time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
		ActivityType: string(timelineActivityRepost),
		SourceID:     42,
	}
	loadUserTimelinePage = func(id uint, limit int, cursor *timelineCursor) (timelinePageResponse, error) {
		if id != userID || limit != 5 || cursor == nil || !cursor.ActivityAt.Equal(wantCursor.ActivityAt) || cursor.ActivityType != wantCursor.ActivityType || cursor.SourceID != wantCursor.SourceID {
			t.Fatalf("loader args id=%d limit=%d cursor=%#v", id, limit, cursor)
		}
		return timelinePageResponse{Items: []timelineItem{}}, nil
	}

	rawCursor, err := encodeTimelineCursor(wantCursor)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newUserControllerContext("/api/users/7/timeline?limit=5&cursor="+rawCursor, "7")
	GetUserTimeline(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetUserTimelineRejectsInvalidIDCursorAndMissingUser(t *testing.T) {
	originalProfileLoader := loadUserTimelineProfile
	originalTimelineLoader := loadUserTimelinePage
	t.Cleanup(func() {
		loadUserTimelineProfile = originalProfileLoader
		loadUserTimelinePage = originalTimelineLoader
	})
	loadUserTimelineProfile = func(uint) (publicUserResponse, error) {
		return publicUserResponse{}, errors.New("profile loader should not be called")
	}
	loadUserTimelinePage = func(uint, int, *timelineCursor) (timelinePageResponse, error) {
		t.Fatal("timeline loader should not be called")
		return timelinePageResponse{}, nil
	}

	for _, test := range []struct {
		path string
		id   string
		want int
	}{
		{path: "/api/users/not-a-number/timeline", id: "not-a-number", want: http.StatusBadRequest},
		{path: "/api/users/7/timeline?cursor=not-base64", id: "7", want: http.StatusBadRequest},
	} {
		ctx, recorder := newUserControllerContext(test.path, test.id)
		GetUserTimeline(ctx)
		if recorder.Code != test.want || !strings.Contains(recorder.Body.String(), `"error"`) {
			t.Fatalf("path=%q status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}

	loadUserTimelineProfile = func(uint) (publicUserResponse, error) {
		return publicUserResponse{}, gorm.ErrRecordNotFound
	}
	ctx, recorder := newUserControllerContext("/api/users/7/timeline", "7")
	GetUserTimeline(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("profile lookup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
