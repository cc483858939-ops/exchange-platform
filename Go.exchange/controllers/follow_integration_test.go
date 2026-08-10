package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newFollowIntegrationContext(method string, viewerID, targetID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/api/users/"+strconvUint(targetID)+"/follow", nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(targetID)}}
	ctx.Set("user_id", viewerID)
	return ctx, recorder
}

func decodeFollowIntegrationState(t *testing.T, recorder *httptest.ResponseRecorder) userFollowStateResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var state userFollowStateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode follow state: %v", err)
	}
	return state
}

func TestFollowGraphIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "follow-alice-" + uuid.NewString(), Password: "test"},
		{Username: "follow-bob-" + uuid.NewString(), Password: "test"},
		{Username: "follow-charlie-" + uuid.NewString(), Password: "test"},
		{Username: "follow-dave-" + uuid.NewString(), Password: "test"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	alice, bob, charlie, dave := users[0], users[1], users[2], users[3]
	userIDs := []uint{alice.ID, bob.ID, charlie.ID, dave.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	ctx, recorder := newFollowIntegrationContext(http.MethodGet, alice.ID, bob.ID)
	GetUserFollowState(ctx)
	state := decodeFollowIntegrationState(t, recorder)
	if state.UserID != bob.ID || state.Following || state.FollowerCount != 0 || state.FollowingCount != 0 {
		t.Fatalf("initial Bob state=%#v", state)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodPut, alice.ID, bob.ID)
	FollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.FollowerCount != 1 {
		t.Fatalf("Alice -> Bob state=%#v", state)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodPut, charlie.ID, bob.ID)
	FollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.FollowerCount != 2 {
		t.Fatalf("Charlie -> Bob state=%#v", state)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodPut, bob.ID, alice.ID)
	FollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.FollowerCount != 1 || state.FollowingCount != 1 {
		t.Fatalf("Bob -> Alice state=%#v", state)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodGet, alice.ID, bob.ID)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.FollowerCount != 2 || state.FollowingCount != 1 {
		t.Fatalf("Bob viewed by Alice state=%#v", state)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodPut, alice.ID, bob.ID)
	FollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.FollowerCount != 2 {
		t.Fatalf("duplicate follow state=%#v", state)
	}
	var pairCount int64
	if err := db.Model(&models.UserFollow{}).
		Where("follower_id = ? AND following_id = ?", alice.ID, bob.ID).
		Count(&pairCount).Error; err != nil {
		t.Fatal(err)
	}
	if pairCount != 1 {
		t.Fatalf("duplicate follow pair count=%d", pairCount)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodGet, alice.ID, alice.ID)
	GetUserFollowState(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if state.Following || state.FollowerCount != 1 || state.FollowingCount != 1 {
		t.Fatalf("self GET state=%#v", state)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodPut, alice.ID, alice.ID)
	FollowUser(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("self PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodDelete, alice.ID, alice.ID)
	UnfollowUser(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("self DELETE status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodDelete, alice.ID, bob.ID)
	UnfollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if state.Following || state.FollowerCount != 1 {
		t.Fatalf("first unfollow state=%#v", state)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodDelete, alice.ID, bob.ID)
	UnfollowUser(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if state.Following || state.FollowerCount != 1 {
		t.Fatalf("duplicate unfollow state=%#v", state)
	}
	if err := db.Model(&models.UserFollow{}).
		Where("follower_id = ? AND following_id = ?", alice.ID, bob.ID).
		Count(&pairCount).Error; err != nil {
		t.Fatal(err)
	}
	if pairCount != 0 {
		t.Fatalf("duplicate unfollow pair count=%d", pairCount)
	}

	ctx, recorder = newFollowIntegrationContext(http.MethodPut, alice.ID, bob.ID)
	FollowUser(ctx)
	decodeFollowIntegrationState(t, recorder)
	if err := db.Delete(&charlie).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodGet, alice.ID, bob.ID)
	state = decodeFollowIntegrationState(t, recorder)
	if state.FollowerCount != 1 {
		t.Fatalf("soft-deleted follower count=%d state=%#v", state.FollowerCount, state)
	}
	if err := db.Unscoped().Where(
		"follower_id = ? AND following_id = ?",
		charlie.ID,
		bob.ID,
	).First(&models.UserFollow{}).Error; err != nil {
		t.Fatalf("soft-deleted follower relation was removed: %v", err)
	}

	if err := db.Delete(&bob).Error; err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		ctx, recorder = newFollowIntegrationContext(method, alice.ID, bob.ID)
		switch method {
		case http.MethodGet:
			GetUserFollowState(ctx)
		case http.MethodPut:
			FollowUser(ctx)
		case http.MethodDelete:
			UnfollowUser(ctx)
		}
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("soft-deleted target method=%s status=%d body=%s", method, recorder.Code, recorder.Body.String())
		}
	}

	if err := db.Delete(&alice).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodPut, alice.ID, dave.ID)
	FollowUser(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("soft-deleted viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
