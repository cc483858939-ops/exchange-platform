package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
)

func TestUserMuteLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.UserMute{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	originalDB := global.APIDb
	global.APIDb = tx
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback mute integration transaction: %v", err)
		}
		global.APIDb = originalDB
	})

	newUser := func(name string) models.User {
		t.Helper()
		user := models.User{Username: "mute-" + name + "-" + uuid.NewString(), Password: "test"}
		if err := tx.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		return user
	}
	viewer, target, third := newUser("viewer"), newUser("target"), newUser("third")
	if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: target.ID}).Error; err != nil {
		t.Fatal(err)
	}
	put := func(targetID uint) int {
		ctx, _ := newUserMuteControllerContext(http.MethodPut, strconvUint(targetID), "", &viewer.ID)
		MuteUser(ctx)
		return ctx.Writer.Status()
	}
	deleteMute := func(targetID uint) int {
		ctx, _ := newUserMuteControllerContext(http.MethodDelete, strconvUint(targetID), "", &viewer.ID)
		UnmuteUser(ctx)
		return ctx.Writer.Status()
	}
	if status := put(target.ID); status != http.StatusNoContent {
		t.Fatalf("mute status=%d", status)
	}
	if status := put(target.ID); status != http.StatusNoContent {
		t.Fatalf("duplicate mute status=%d", status)
	}
	var relationCount int64
	if err := tx.Model(&models.UserMute{}).Where("muter_id = ? AND muted_id = ?", viewer.ID, target.ID).Count(&relationCount).Error; err != nil || relationCount != 1 {
		t.Fatalf("duplicate relation count=%d err=%v", relationCount, err)
	}

	ctx, recorder := newFollowIntegrationContext(http.MethodGet, viewer.ID, target.ID)
	GetUserFollowState(ctx)
	state := decodeFollowIntegrationState(t, recorder)
	if !state.Following || !state.Muted {
		t.Fatalf("follow and mute state=%#v, want both true", state)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodGet, third.ID, target.ID)
	GetUserFollowState(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if state.Muted {
		t.Fatal("another viewer inherited the mute relation")
	}

	if status := put(viewer.ID); status != http.StatusBadRequest {
		t.Fatalf("self mute status=%d", status)
	}
	if status := put(999999999); status != http.StatusNotFound {
		t.Fatalf("missing target mute status=%d", status)
	}
	deleted := newUser("deleted")
	if err := tx.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if status := put(deleted.ID); status != http.StatusNotFound {
		t.Fatalf("soft-deleted target mute status=%d", status)
	}

	if status := put(third.ID); status != http.StatusNoContent {
		t.Fatalf("second target mute status=%d", status)
	}
	firstPageCtx, firstPageRecorder := newUserMuteControllerContext(http.MethodGet, "", "limit=1", &viewer.ID)
	GetMyMutedUsers(firstPageCtx)
	var firstPage userMutePageResponse
	if firstPageRecorder.Code != http.StatusOK || json.Unmarshal(firstPageRecorder.Body.Bytes(), &firstPage) != nil || len(firstPage.Items) != 1 || firstPage.NextCursor == nil {
		t.Fatalf("first mute page status=%d body=%s page=%#v", firstPageRecorder.Code, firstPageRecorder.Body.String(), firstPage)
	}
	secondPageCtx, secondPageRecorder := newUserMuteControllerContext(http.MethodGet, "", "limit=1&cursor="+*firstPage.NextCursor, &viewer.ID)
	GetMyMutedUsers(secondPageCtx)
	var secondPage userMutePageResponse
	if secondPageRecorder.Code != http.StatusOK || json.Unmarshal(secondPageRecorder.Body.Bytes(), &secondPage) != nil || len(secondPage.Items) != 1 || secondPage.NextCursor != nil || secondPage.Items[0].User.ID == firstPage.Items[0].User.ID {
		t.Fatalf("second mute page status=%d body=%s page=%#v", secondPageRecorder.Code, secondPageRecorder.Body.String(), secondPage)
	}

	if status := deleteMute(target.ID); status != http.StatusNoContent {
		t.Fatalf("unmute status=%d", status)
	}
	if status := deleteMute(target.ID); status != http.StatusNoContent {
		t.Fatalf("duplicate unmute status=%d", status)
	}
	ctx, recorder = newFollowIntegrationContext(http.MethodGet, viewer.ID, target.ID)
	GetUserFollowState(ctx)
	state = decodeFollowIntegrationState(t, recorder)
	if !state.Following || state.Muted {
		t.Fatalf("state after unmute=%#v, want following=true and muted=false", state)
	}
}
