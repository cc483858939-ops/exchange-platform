package controllers

import (
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

func TestDecodeUserProfilePatchValidation(t *testing.T) {
	validAvatar := "/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.jpg"
	invalidBodies := []string{
		"{}",
		"{\"website\":\"example\"}",
		"{\"username\":\"new\"}",
		"{\"username\":null}",
		"{\"display_name\":null}",
		"{\"display_name\":12}",
		"{\"bio\":null}",
		"{\"bio\":[]}",
		"{\"avatar_url\":null}",
		"{\"avatar_url\":false}",
		"{\"avatar_url\":\"/api/files/profile-avatars/99/550e8400-e29b-41d4-a716-446655440000.jpg\"}",
		"{\"avatar_url\":\"https://example.com/avatar.jpg\"}",
		"{\"avatar_url\":\"/api/files/article-covers/550e8400-e29b-41d4-a716-446655440000.jpg\"}",
		"{\"avatar_url\":\"/api/files/profile-avatars/42/../550e8400-e29b-41d4-a716-446655440000.jpg\"}",
		"{\"avatar_url\":\"/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.jpg\\r\\n\"}",
		"{\"avatar_url\":\"/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.gif\"}",
		"{\"avatar_url\":\"/api/files/profile-avatars/42/not-a-uuid.jpg\"}",
		"{\"avatar_url\":\"/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000/extra.jpg\"}",
	}
	for _, body := range invalidBodies {
		if _, err := decodeUserProfilePatch(strings.NewReader(body), 42); err == nil {
			t.Fatalf("accepted invalid body: %s", body)
		}
	}

	updates, err := decodeUserProfilePatch(strings.NewReader("{\"display_name\":\"  Alice Chen  \",\"bio\":\"  FX\\nrates  \",\"avatar_url\":\"\"}"), 42)
	if err != nil || updates["display_name"] != "Alice Chen" || updates["bio"] != "FX\nrates" || updates["avatar_url"] != "" {
		t.Fatalf("unexpected normalized update: %#v err=%v", updates, err)
	}
	partial, err := decodeUserProfilePatch(strings.NewReader("{\"bio\":\"FX\"}"), 42)
	if err != nil || len(partial) != 1 || partial["bio"] != "FX" {
		t.Fatalf("unexpected partial update: %#v err=%v", partial, err)
	}
	avatarOnly, err := decodeUserProfilePatch(strings.NewReader("{\"avatar_url\":\""+validAvatar+"\"}"), 42)
	if err != nil || avatarOnly["avatar_url"] != validAvatar {
		t.Fatalf("unexpected valid avatar update: %#v err=%v", avatarOnly, err)
	}
}
func TestDecodeUserProfilePatchUnicodeLimits(t *testing.T) {
	display50 := strings.Repeat("界", 50)
	display51 := strings.Repeat("界", 51)
	bio160 := strings.Repeat("界", 160)
	bio161 := strings.Repeat("界", 161)
	if _, err := decodeUserProfilePatch(strings.NewReader("{\"display_name\":\""+display50+"\"}"), 42); err != nil {
		t.Fatalf("50-rune display name rejected: %v", err)
	}
	if _, err := decodeUserProfilePatch(strings.NewReader("{\"display_name\":\""+display51+"\"}"), 42); err == nil {
		t.Fatal("51-rune display name accepted")
	}
	if _, err := decodeUserProfilePatch(strings.NewReader("{\"bio\":\""+bio160+"\"}"), 42); err != nil {
		t.Fatalf("160-rune bio rejected: %v", err)
	}
	if _, err := decodeUserProfilePatch(strings.NewReader("{\"bio\":\""+bio161+"\"}"), 42); err == nil {
		t.Fatal("161-rune bio accepted")
	}
}

func TestUpdateUserProfileAuthorizationOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := loadActiveProfileViewer
	t.Cleanup(func() { loadActiveProfileViewer = original })

	loadActiveProfileViewer = func(uint) (models.User, error) {
		t.Fatal("active lookup should not run without viewer identity")
		return models.User{}, nil
	}
	ctx, recorder := newProfilePatchUnitContext("42", "{}", nil)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status=%d", recorder.Code)
	}

	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, gorm.ErrRecordNotFound }
	ctx, recorder = newProfilePatchUnitContext("42", "{\"bio\":\"FX\"}", uint(42))
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d", recorder.Code)
	}

	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{Model: gorm.Model{ID: 42}}, nil }
	ctx, recorder = newProfilePatchUnitContext("43", "{}", uint(42))
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("target mismatch status=%d", recorder.Code)
	}

	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, errors.New("db unavailable") }
	ctx, recorder = newProfilePatchUnitContext("42", "{\"bio\":\"FX\"}", uint(42))
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("active lookup failure status=%d", recorder.Code)
	}
}
func newProfilePatchUnitContext(targetID, body string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/users/"+targetID, strings.NewReader(body))
	ctx.Params = gin.Params{{Key: "id", Value: targetID}}
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}

func TestSearchUsersContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalSearch := searchUsers
	t.Cleanup(func() { loadActiveProfileViewer = originalActive; searchUsers = originalSearch })
	loadActiveProfileViewer = func(id uint) (models.User, error) { return models.User{Model: gorm.Model{ID: id}}, nil }

	var receivedViewer uint
	var receivedQuery string
	var receivedLimit, receivedOffset int
	searchUsers = func(viewerID uint, query string, limit, offset int) (userConnectionPageResponse, error) {
		receivedViewer, receivedQuery, receivedLimit, receivedOffset = viewerID, query, limit, offset
		return userConnectionPageResponse{Items: []userConnectionResponse{{User: publicUserResponse{ID: 7, Username: "alice", DisplayName: "Alice"}, Following: true}}, HasMore: true}, nil
	}
	ctx, recorder := newUserSearchUnitContext("?q=%20@Alice%20", uint(42))
	SearchUsers(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if receivedViewer != 42 || receivedQuery != "Alice" || receivedLimit != 20 || receivedOffset != 0 {
		t.Fatalf("loader args=%d %q %d %d", receivedViewer, receivedQuery, receivedLimit, receivedOffset)
	}
	var page userConnectionPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 1 || page.Items[0].User.Username != "alice" || !page.Items[0].Following {
		t.Fatalf("page=%#v", page)
	}

	ctx, recorder = newUserSearchUnitContext("?q=alice&limit=99&offset=4", uint(42))
	SearchUsers(ctx)
	if recorder.Code != http.StatusOK || receivedLimit != 50 || receivedOffset != 4 {
		t.Fatalf("clamp status=%d args=%d,%d", recorder.Code, receivedLimit, receivedOffset)
	}

	for _, raw := range []string{"", "?q=", "?q=%20%20", "?q=@", "?q=alice&limit=0", "?q=alice&limit=nope", "?q=alice&offset=-1", "?q=alice&offset=nope"} {
		ctx, recorder = newUserSearchUnitContext(raw, uint(42))
		SearchUsers(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q status=%d", raw, recorder.Code)
		}
	}
	ctx, recorder = newUserSearchUnitContext("?q="+strings.Repeat("?", maxUserSearchQueryRunes+1), uint(42))
	SearchUsers(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("overlong status=%d", recorder.Code)
	}
}

func TestSearchUsersAuthenticationAndStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalSearch := searchUsers
	t.Cleanup(func() { loadActiveProfileViewer = originalActive; searchUsers = originalSearch })

	loadActiveProfileViewer = func(uint) (models.User, error) {
		t.Fatal("active lookup should not run without viewer")
		return models.User{}, nil
	}
	ctx, recorder := newUserSearchUnitContext("?q=alice", nil)
	SearchUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status=%d", recorder.Code)
	}

	loadActiveProfileViewer = func(uint) (models.User, error) { return models.User{}, gorm.ErrRecordNotFound }
	ctx, recorder = newUserSearchUnitContext("?q=alice", uint(9))
	SearchUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d", recorder.Code)
	}

	loadActiveProfileViewer = func(id uint) (models.User, error) { return models.User{Model: gorm.Model{ID: id}}, nil }
	searchUsers = func(uint, string, int, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, errors.New("database exploded")
	}
	ctx, recorder = newUserSearchUnitContext("?q=alice", uint(9))
	SearchUsers(ctx)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "internal server error") || strings.Contains(recorder.Body.String(), "exploded") {
		t.Fatalf("store error status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	searchUsers = func(uint, string, int, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, nil
	}
	ctx, recorder = newUserSearchUnitContext("?q=alice", uint(9))
	SearchUsers(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"items\":[]") || !strings.Contains(recorder.Body.String(), "\"has_more\":false") {
		t.Fatalf("empty page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newUserSearchUnitContext(rawQuery string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/users/search"+rawQuery, nil)
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}
