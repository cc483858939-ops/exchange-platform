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

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newLikedHistoryTestContext(path string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}

func TestGetMyLikedHistoryCanonicalResponseAndLimits(t *testing.T) {
	viewerID := uint(17)
	originalActive := loadActiveProfileViewer
	originalLoader := loadLikedHistoryPage
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadLikedHistoryPage = originalLoader
	})

	loadActiveProfileViewer = func(id uint) (models.User, error) {
		if id != viewerID {
			t.Fatalf("active viewer id=%d", id)
		}
		return models.User{}, nil
	}
	var limits []int
	loadLikedHistoryPage = func(id uint, limit int, cursor *likedHistoryCursor) (articlePageResponse, error) {
		if id != viewerID || cursor != nil {
			t.Fatalf("loader args id=%d limit=%d cursor=%v", id, limit, cursor)
		}
		limits = append(limits, limit)
		return articlePageResponse{Items: []articleResponse{}}, nil
	}

	ctx, recorder := newLikedHistoryTestContext("/api/me/history/likes", viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("default status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newLikedHistoryTestContext("/api/me/history/likes?limit=100", viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clamped status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(limits) != 2 || limits[0] != defaultLikedHistoryLimit || limits[1] != maxLikedHistoryLimit {
		t.Fatalf("limits=%v", limits)
	}
}

func TestGetMyLikedHistoryRejectsInvalidLimitAndCursor(t *testing.T) {
	viewerID := uint(17)
	originalActive := loadActiveProfileViewer
	originalLoader := loadLikedHistoryPage
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadLikedHistoryPage = originalLoader
	})
	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, nil }
	loadLikedHistoryPage = func(uint, int, *likedHistoryCursor) (articlePageResponse, error) {
		t.Fatal("liked history loader should not be called")
		return articlePageResponse{}, nil
	}

	for _, raw := range []string{"0", "-1", "not-a-number"} {
		ctx, recorder := newLikedHistoryTestContext("/api/me/history/likes?limit="+raw, viewerID)
		GetMyLikedHistory(ctx)
		if recorder.Code != http.StatusBadRequest || strings.TrimSpace(recorder.Body.String()) != `{"error":"invalid limit"}` {
			t.Fatalf("limit=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}

	zeroTime, err := json.Marshal(likedHistoryCursor{Version: likedHistoryCursorVersion, ArticleID: 1})
	if err != nil {
		t.Fatal(err)
	}
	zeroID, err := json.Marshal(likedHistoryCursor{
		Version:        likedHistoryCursorVersion,
		StateChangedAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC),
	})
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
		ctx, recorder := newLikedHistoryTestContext("/api/me/history/likes?cursor="+raw, viewerID)
		GetMyLikedHistory(ctx)
		if recorder.Code != http.StatusBadRequest || strings.TrimSpace(recorder.Body.String()) != `{"error":"invalid cursor"}` {
			t.Fatalf("cursor=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGetMyLikedHistoryAuthenticationAndLoaderErrors(t *testing.T) {
	originalActive := loadActiveProfileViewer
	originalLoader := loadLikedHistoryPage
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadLikedHistoryPage = originalLoader
	})
	loadLikedHistoryPage = func(uint, int, *likedHistoryCursor) (articlePageResponse, error) {
		t.Fatal("liked history loader should not be called")
		return articlePageResponse{}, nil
	}

	loadActiveProfileViewer = func(uint) (models.User, error) {
		t.Fatal("active lookup should not run without viewer identity")
		return models.User{}, nil
	}
	ctx, recorder := newLikedHistoryTestContext("/api/me/history/likes", nil)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	viewerID := uint(17)
	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, gorm.ErrRecordNotFound }
	ctx, recorder = newLikedHistoryTestContext("/api/me/history/likes", viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, errors.New("database unavailable") }
	ctx, recorder = newLikedHistoryTestContext("/api/me/history/likes", viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("viewer failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(id uint) (models.User, error) { return models.User{Model: gorm.Model{ID: id}}, nil }
	loadLikedHistoryPage = func(uint, int, *likedHistoryCursor) (articlePageResponse, error) {
		return articlePageResponse{}, errors.New("query failed")
	}
	ctx, recorder = newLikedHistoryTestContext("/api/me/history/likes", viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "query failed") {
		t.Fatalf("loader failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLikedHistoryCursorRoundTripAndValidation(t *testing.T) {
	want := likedHistoryCursor{
		Version:        likedHistoryCursorVersion,
		StateChangedAt: time.Date(2026, 8, 10, 14, 0, 0, 123456000, time.UTC),
		ArticleID:      42,
	}
	raw, err := encodeLikedHistoryCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeLikedHistoryCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StateChangedAt.Equal(want.StateChangedAt) || got.ArticleID != want.ArticleID || got.Version != want.Version {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
	if _, err := encodeLikedHistoryCursor(likedHistoryCursor{}); err == nil {
		t.Fatal("expected zero cursor encoding error")
	}
}

func TestGetMyLikedHistoryPassesDedicatedCursorToLoader(t *testing.T) {
	viewerID := uint(17)
	originalActive := loadActiveProfileViewer
	originalLoader := loadLikedHistoryPage
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadLikedHistoryPage = originalLoader
	})
	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, nil }
	want := likedHistoryCursor{
		Version:        likedHistoryCursorVersion,
		StateChangedAt: time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC),
		ArticleID:      42,
	}
	raw, err := encodeLikedHistoryCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	var received *likedHistoryCursor
	loadLikedHistoryPage = func(id uint, limit int, cursor *likedHistoryCursor) (articlePageResponse, error) {
		if id != viewerID || limit != 20 {
			t.Fatalf("loader args id=%d limit=%d", id, limit)
		}
		received = cursor
		return articlePageResponse{Items: []articleResponse{}}, nil
	}

	ctx, recorder := newLikedHistoryTestContext("/api/me/history/likes?cursor="+raw, viewerID)
	GetMyLikedHistory(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if received == nil || received.Version != want.Version || received.ArticleID != want.ArticleID || !received.StateChangedAt.Equal(want.StateChangedAt) {
		t.Fatalf("received cursor=%v", received)
	}
}
