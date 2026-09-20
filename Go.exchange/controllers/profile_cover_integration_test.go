package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/profilecover"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestProfileCoverUserContractIntegration(t *testing.T) {
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

	target := models.User{Username: "cover-contract-target-" + uuid.NewString(), Password: "secret"}
	other := models.User{Username: "cover-contract-other-" + uuid.NewString(), Password: "secret"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", []uint{target.ID, other.ID}).Delete(&models.User{})
	})

	getContext, getRecorder := newUserControllerContext("/api/users/"+strconvUint(target.ID), strconvUint(target.ID))
	GetUserByID(getContext)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("initial GET status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial publicUserResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.CoverImageURL != "" {
		t.Fatalf("new user cover=%q want empty", initial.CoverImageURL)
	}

	hash := strings.Repeat("a", 64)
	coverURL := profilecover.FilesURLPrefix + profilecover.UserV1ObjectPrefix + strconvUint(target.ID) + "/" + hash + ".jpg"
	ctx, recorder := newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"cover_image_url":"`+coverURL+`"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cover PATCH status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated publicUserResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.CoverImageURL != coverURL {
		t.Fatalf("PATCH cover=%q want=%q", updated.CoverImageURL, coverURL)
	}
	var persisted models.User
	if err := db.First(&persisted, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.CoverImageURL != coverURL {
		t.Fatalf("persisted cover=%q want=%q", persisted.CoverImageURL, coverURL)
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"cover_image_url":""}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cover removal status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.CoverImageURL != "" {
		t.Fatalf("removed cover=%q", updated.CoverImageURL)
	}

	wrongOwnerURL := profilecover.FilesURLPrefix + profilecover.UserV1ObjectPrefix + strconvUint(other.ID) + "/" + hash + ".jpg"
	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"cover_image_url":"`+wrongOwnerURL+`"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong-owner cover status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(other.ID), `{"cover_image_url":"`+coverURL+`"}`, other.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("cross-user cover assignment status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
