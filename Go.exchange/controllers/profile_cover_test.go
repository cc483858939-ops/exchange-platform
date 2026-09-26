package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/models"
	"Go.exchange/profilecover"
	"Go.exchange/profilecoverimage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestDecodeUserProfilePatchCoverURLContract(t *testing.T) {
	hash := strings.Repeat("a", 64)
	valid := profilecover.FilesURLPrefix + profilecover.UserV1ObjectPrefix + "42/" + hash + ".jpg"
	updates, err := decodeUserProfilePatch(strings.NewReader(`{"cover_image_url":"`+valid+`"}`), 42)
	if err != nil || updates["cover_image_url"] != valid {
		t.Fatalf("valid cover update=%#v err=%v", updates, err)
	}
	removed, err := decodeUserProfilePatch(strings.NewReader(`{"cover_image_url":""}`), 42)
	if err != nil || removed["cover_image_url"] != "" {
		t.Fatalf("cover removal=%#v err=%v", removed, err)
	}
	for _, body := range []string{
		`{"cover_image_url":null}`,
		`{"cover_image_url":12}`,
		`{"cover_image_url":"https://example.com/cover.jpg"}`,
		`{"cover_image_url":"` + profilecover.FilesURLPrefix + profilecover.UserV1ObjectPrefix + "99/" + hash + `.jpg"}`,
		`{"cover_image_url":"` + profilecover.FilesURLPrefix + profilecover.UserV1ObjectPrefix + "42/" + hash + `.webp"}`,
	} {
		if _, err := decodeUserProfilePatch(strings.NewReader(body), 42); err == nil {
			t.Fatalf("invalid cover patch accepted: %s", body)
		}
	}
}

func TestUploadProfileCoverStoresDerivativeAndReusesExactObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	originalStat := statProfileCoverObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
		statProfileCoverObject = originalStat
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	payload := controllerProfileCoverJPEGFixture(t)
	derivative, err := profilecoverimage.Optimize(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, err := profilecover.BuildUserV1ObjectKey(42, derivative.ContentHash, derivative.Extension)
	if err != nil {
		t.Fatal(err)
	}

	var putCount int
	var gotKey string
	putStoredObject = func(_ context.Context, objectKey string, reader io.Reader, size int64, contentType string) error {
		putCount++
		gotKey = objectKey
		body, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if int64(len(body)) != size || !strings.HasPrefix(contentType, "image/") {
			t.Fatalf("stored derivative metadata size=%d body=%d contentType=%q", size, len(body), contentType)
		}
		return nil
	}
	statProfileCoverObject = func(context.Context, string) (storedObjectInfo, bool, error) {
		return storedObjectInfo{}, false, nil
	}

	call := func() *httptest.ResponseRecorder {
		body, contentType := multipartImageRequestBody(t, "cover.bin", payload)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-cover", body)
		ctx.Request.Header.Set("Content-Type", contentType)
		ctx.Set("user_id", uint(42))
		UploadProfileCover(ctx)
		return recorder
	}
	response := call()
	if response.Code != http.StatusOK || gotKey != key || putCount != 1 {
		t.Fatalf("first upload status=%d key=%q puts=%d body=%s", response.Code, gotKey, putCount, response.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["cover_image_url"] != profilecover.FilesURLPrefix+key {
		t.Fatalf("response=%#v", result)
	}
	statProfileCoverObject = func(_ context.Context, got string) (storedObjectInfo, bool, error) {
		if got != key {
			t.Fatalf("stat key=%q want=%q", got, key)
		}
		return storedObjectInfo{Size: int64(len(derivative.Body)), ContentType: derivative.ContentType}, true, nil
	}
	response = call()
	if response.Code != http.StatusOK || putCount != 1 {
		t.Fatalf("duplicate upload status=%d puts=%d body=%s", response.Code, putCount, response.Body.String())
	}
}

func TestUploadProfileCoverRejectsInvalidOrOversizedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error {
		t.Fatal("invalid input reached storage")
		return nil
	}
	for _, payload := range [][]byte{nil, []byte("not an image"), make([]byte, profilecoverimage.MaxSourceBytes+1)} {
		body, contentType := multipartImageRequestBody(t, "cover.bin", payload)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-cover", body)
		ctx.Request.Header.Set("Content-Type", contentType)
		ctx.Set("user_id", uint(42))
		UploadProfileCover(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("payload size=%d status=%d body=%s", len(payload), recorder.Code, recorder.Body.String())
		}
	}
}

func TestProfileCoverFileAllowlistAndCacheContract(t *testing.T) {
	valid := "profile-covers/users/v1/42/" + strings.Repeat("a", 64) + ".jpg"
	if !isAllowedObjectKey(valid) {
		t.Fatal("valid cover object key rejected")
	}
	if got := fileCacheControl(valid); got != "public, max-age=31536000, immutable" {
		t.Fatalf("cache control=%q", got)
	}
	for _, invalid := range []string{
		"profile-covers/users/v1/42/" + strings.Repeat("A", 64) + ".jpg",
		"profile-covers/users/v1/42/" + strings.Repeat("a", 64) + ".webp",
		"profile-covers/users/v1/42/../cover.jpg",
		"profile-covers/users/v1/420/" + strings.Repeat("a", 64) + ".jpg\r\n",
	} {
		if isAllowedObjectKey(invalid) {
			t.Fatalf("invalid cover object key accepted: %q", invalid)
		}
	}
}

func controllerProfileCoverJPEGFixture(t *testing.T) []byte {
	return profileJPEGFixture(t)
}
