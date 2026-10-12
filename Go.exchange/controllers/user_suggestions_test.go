package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestGetSuggestedUsersContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalLoader := loadSuggestedUsers
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadSuggestedUsers = originalLoader
	})
	loadActiveProfileViewer = func(_ context.Context, id uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: id}}, nil
	}

	var gotViewer uint
	var gotLimit int
	loadSuggestedUsers = func(_ context.Context, viewerID uint, limit int) (userConnectionPageResponse, error) {
		gotViewer, gotLimit = viewerID, limit
		return userConnectionPageResponse{Items: []userConnectionResponse{{
			User: publicUserSummaryResponse{ID: 19, Username: "alice", DisplayName: "Alice"},
		}}, HasMore: false}, nil
	}

	ctx, recorder := newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || gotViewer != 42 || gotLimit != defaultSuggestedUserLimit {
		t.Fatalf("default status=%d viewer=%d limit=%d body=%s", recorder.Code, gotViewer, gotLimit, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["has_more"] != false {
		t.Fatalf("response=%v", payload)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items=%v", payload["items"])
	}
	user, ok := items[0].(map[string]any)["user"].(map[string]any)
	if !ok || user["username"] != "alice" {
		t.Fatalf("user response=%v", items[0])
	}
	for _, privateField := range []string{"password", "cover_image_url", "follower_count"} {
		if _, exists := user[privateField]; exists {
			t.Fatalf("response exposed %q: %v", privateField, user)
		}
	}

	ctx, recorder = newSuggestedUsersUnitContext("?limit=5", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || gotLimit != maxSuggestedUserLimit {
		t.Fatalf("max limit status=%d got=%d body=%s", recorder.Code, gotLimit, recorder.Body.String())
	}

	for _, query := range []string{"?limit=", "?limit=0", "?limit=-1", "?limit=6", "?limit=nope", "?limit=3&limit=4", "?offset=0"} {
		ctx, recorder = newSuggestedUsersUnitContext(query, uint(42))
		GetSuggestedUsers(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d body=%s, want 400", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGetSuggestedUsersAuthenticationAndDatabaseErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalLoader := loadSuggestedUsers
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadSuggestedUsers = originalLoader
	})
	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		t.Fatal("suggestion query should not run")
		return userConnectionPageResponse{}, nil
	}

	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{}, errors.New("inactive lookup unavailable")
	}
	ctx, recorder := newSuggestedUsersUnitContext("", nil)
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newSuggestedUsersUnitContext("", true)
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid viewer identity status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{}, gorm.ErrRecordNotFound
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(_ context.Context, id uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: id}}, nil
	}
	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, errors.New("database exploded")
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "exploded") {
		t.Fatalf("query error status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, nil
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items":[]`) || !strings.Contains(recorder.Body.String(), `"has_more":false`) {
		t.Fatalf("empty response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newSuggestedUsersUnitContext(query string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/users/suggestions"+query, nil)
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}
