package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newUserMuteControllerContext(method, targetID, query string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/api/users/" + targetID + "/mute"
	if method == http.MethodGet {
		path = "/api/me/mutes"
	}
	if query != "" {
		path += "?" + query
	}
	ctx.Request = httptest.NewRequest(method, path, nil)
	if targetID != "" {
		ctx.Params = gin.Params{{Key: "id", Value: targetID}}
	}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func TestMuteUserIsIdempotentAndRejectsInvalidTargets(t *testing.T) {
	viewerID := uint(7)
	originalLoadViewer := loadActiveProfileViewer
	originalLoadTarget := loadActiveMuteUser
	originalCreate := createUserMute
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoadViewer
		loadActiveMuteUser = originalLoadTarget
		createUserMute = originalCreate
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) { return models.User{}, nil }
	loadActiveMuteUser = func(_ context.Context, id uint) error {
		if id == 999 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	created := 0
	createUserMute = func(_ context.Context, viewer, target uint) error {
		if viewer != viewerID || target != 42 {
			t.Fatalf("mute relation=%d -> %d", viewer, target)
		}
		created++
		return nil
	}

	for index := 0; index < 2; index++ {
		ctx, recorder := newUserMuteControllerContext(http.MethodPut, "42", "", &viewerID)
		MuteUser(ctx)
		if ctx.Writer.Status() != http.StatusNoContent {
			t.Fatalf("mute status=%d body=%s", ctx.Writer.Status(), recorder.Body.String())
		}
	}
	if created != 2 {
		t.Fatalf("idempotent requests should reach the unique-index upsert twice, got %d", created)
	}

	ctx, recorder := newUserMuteControllerContext(http.MethodPut, "7", "", &viewerID)
	MuteUser(ctx)
	if recorder.Code != http.StatusBadRequest || created != 2 {
		t.Fatalf("self mute status=%d creates=%d", recorder.Code, created)
	}
	ctx, recorder = newUserMuteControllerContext(http.MethodPut, "999", "", &viewerID)
	MuteUser(ctx)
	if recorder.Code != http.StatusNotFound || created != 2 {
		t.Fatalf("missing target status=%d creates=%d", recorder.Code, created)
	}
	ctx, recorder = newUserMuteControllerContext(http.MethodPut, "42", "", nil)
	MuteUser(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d", recorder.Code)
	}
}

func TestUnmuteUserDeletesOnlyAuthenticatedViewerRelation(t *testing.T) {
	viewerID := uint(11)
	originalLoadViewer := loadActiveProfileViewer
	originalDelete := deleteUserMute
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoadViewer
		deleteUserMute = originalDelete
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) { return models.User{}, nil }
	var gotViewer, gotTarget uint
	deleteUserMute = func(_ context.Context, viewer, target uint) error {
		gotViewer, gotTarget = viewer, target
		return nil
	}
	for index := 0; index < 2; index++ {
		ctx, recorder := newUserMuteControllerContext(http.MethodDelete, "42", "", &viewerID)
		UnmuteUser(ctx)
		if ctx.Writer.Status() != http.StatusNoContent {
			t.Fatalf("unmute status=%d body=%s", ctx.Writer.Status(), recorder.Body.String())
		}
	}
	if gotViewer != viewerID || gotTarget != 42 {
		t.Fatalf("delete relation=%d -> %d", gotViewer, gotTarget)
	}
}

func TestGetMyMutedUsersUsesBoundedCursorPage(t *testing.T) {
	viewerID := uint(19)
	originalLoadViewer := loadActiveProfileViewer
	originalLoad := loadUserMutes
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoadViewer
		loadUserMutes = originalLoad
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) { return models.User{}, nil }
	createdAt := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	cursorToken, err := encodeUserMuteCursor(userMuteCursor{ID: 5, CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	loadUserMutes = func(_ context.Context, viewer uint, limit int, cursor *userMuteCursor) (userMutePageResponse, error) {
		if viewer != viewerID || limit != 7 || cursor == nil || cursor.ID != 5 || !cursor.CreatedAt.Equal(createdAt) {
			t.Fatalf("list args viewer=%d limit=%d cursor=%#v", viewer, limit, cursor)
		}
		return userMutePageResponse{Items: []userMuteItemResponse{{User: mutedUserSummary{ID: 42, Username: "alice"}, CreatedAt: createdAt}}}, nil
	}
	ctx, recorder := newUserMuteControllerContext(http.MethodGet, "", "limit=7&cursor="+cursorToken, &viewerID)
	GetMyMutedUsers(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page userMutePageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].User.ID != 42 {
		t.Fatalf("list response=%s page=%#v err=%v", recorder.Body.String(), page, err)
	}
}
