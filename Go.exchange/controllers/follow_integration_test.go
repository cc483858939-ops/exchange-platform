package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
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

	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{
		Kafka: config.KafkaConfig{
			ActivityEventsTopic: "goexchange.activity.events.v1",
		},
	}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

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
	GetUserFollowState(ctx)
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
	GetUserFollowState(ctx)
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

func newFollowConnectionListIntegrationContext(viewerID, targetID uint, connectionPath, query string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/users/"+strconvUint(targetID)+"/"+connectionPath+query, nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(targetID)}}
	ctx.Set("user_id", viewerID)
	return ctx, recorder
}

func decodeFollowConnectionListPage(t *testing.T, recorder *httptest.ResponseRecorder) userConnectionPageResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page userConnectionPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Items == nil {
		t.Fatal("items must be [] rather than null")
	}
	return page
}

func TestFollowConnectionListsIntegration(t *testing.T) {
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
	newUser := func(label string) models.User {
		return models.User{Username: "connections-" + label + "-" + uuid.NewString(), Password: "test", DisplayName: label}
	}
	users := []models.User{newUser("alice"), newUser("bob"), newUser("charlie"), newUser("dave"), newUser("deleted-follower"), newUser("deleted-following")}
	for index := 0; index < 20; index += 1 {
		users = append(users, newUser("page-"+strconvUint(uint(index))))
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	alice, bob, charlie, dave := users[0], users[1], users[2], users[3]
	deletedFollower, deletedFollowing := users[4], users[5]
	userIDs := make([]uint, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})
	tie := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	older := tie.Add(-time.Hour)
	relations := []models.UserFollow{
		{FollowerID: charlie.ID, FollowingID: bob.ID, CreatedAt: tie}, {FollowerID: alice.ID, FollowingID: bob.ID, CreatedAt: tie}, {FollowerID: deletedFollower.ID, FollowingID: bob.ID, CreatedAt: older},
		{FollowerID: bob.ID, FollowingID: charlie.ID, CreatedAt: tie}, {FollowerID: bob.ID, FollowingID: dave.ID, CreatedAt: tie}, {FollowerID: alice.ID, FollowingID: charlie.ID, CreatedAt: older}, {FollowerID: bob.ID, FollowingID: deletedFollowing.ID, CreatedAt: older},
	}
	for _, user := range users[6:] {
		relations = append(relations, models.UserFollow{FollowerID: bob.ID, FollowingID: user.ID, CreatedAt: older})
	}
	if err := db.Create(&relations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedFollower).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedFollowing).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newFollowConnectionListIntegrationContext(alice.ID, bob.ID, "followers", "")
	GetUserFollowers(ctx)
	followers := decodeFollowConnectionListPage(t, recorder)
	if followers.HasMore || len(followers.Items) != 2 || followers.Items[0].User.ID != alice.ID || followers.Items[0].Following || followers.Items[1].User.ID != charlie.ID || !followers.Items[1].Following {
		t.Fatalf("followers=%#v", followers)
	}
	ctx, recorder = newFollowConnectionListIntegrationContext(alice.ID, bob.ID, "following", "?limit=2")
	GetUserFollowing(ctx)
	following := decodeFollowConnectionListPage(t, recorder)
	if !following.HasMore || len(following.Items) != 2 || following.Items[0].User.ID != dave.ID || following.Items[0].Following || following.Items[1].User.ID != charlie.ID || !following.Items[1].Following {
		t.Fatalf("following=%#v", following)
	}
	ctx, recorder = newFollowConnectionListIntegrationContext(alice.ID, bob.ID, "following", "?limit=20&offset=2")
	GetUserFollowing(ctx)
	followingAfterOffset := decodeFollowConnectionListPage(t, recorder)
	if followingAfterOffset.HasMore || len(followingAfterOffset.Items) != 20 {
		t.Fatalf("following offset=%#v", followingAfterOffset)
	}
	if err := db.Delete(&bob).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newFollowConnectionListIntegrationContext(alice.ID, bob.ID, "followers", "")
	GetUserFollowers(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("soft-deleted target status=%d", recorder.Code)
	}
	ctx, recorder = newFollowConnectionListIntegrationContext(alice.ID, charlie.ID, "followers", "")
	GetUserFollowers(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("active target status=%d", recorder.Code)
	}
	if err := db.Delete(&alice).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newFollowConnectionListIntegrationContext(alice.ID, charlie.ID, "followers", "")
	GetUserFollowers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("soft-deleted viewer status=%d", recorder.Code)
	}
}
