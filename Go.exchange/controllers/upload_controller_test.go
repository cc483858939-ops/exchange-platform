package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/avatarimage"
	"Go.exchange/models"
	"Go.exchange/profileavatar"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestUploadPostMediaStoresImageAndReturnsURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalLoader := loadActiveProfileViewer
	originalPutStoredObject := putStoredObject
	defer func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPutStoredObject
	}()
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}

	pngPayload := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("payload")...)
	var gotObjectKey string
	var gotContentType string
	var gotObjectSize int64
	var gotPayload []byte
	putStoredObject = func(ctx context.Context, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
		var err error
		gotObjectKey = objectKey
		gotContentType = contentType
		gotObjectSize = objectSize
		gotPayload, err = io.ReadAll(reader)
		return err
	}

	body, contentType := multipartImageRequestBody(t, "post.png", pngPayload)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/post-media", body)
	ctx.Request.Header.Set("Content-Type", contentType)
	ctx.Set("user_id", uint(42))

	UploadPostMedia(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(gotObjectKey, postMediaObjectPrefix+"42/") || !strings.HasSuffix(gotObjectKey, ".png") {
		t.Fatalf("unexpected object key: %s", gotObjectKey)
	}
	if gotContentType != "image/png" {
		t.Fatalf("unexpected content type: %s", gotContentType)
	}
	if gotObjectSize != int64(len(pngPayload)) {
		t.Fatalf("unexpected object size: got %d want %d", gotObjectSize, len(pngPayload))
	}
	if !bytes.Equal(gotPayload, pngPayload) {
		t.Fatal("uploaded payload was not preserved")
	}

	var response postMediaUploadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(response.MediaURL, "/api/files/"+postMediaObjectPrefix+"42/") {
		t.Fatalf("unexpected media url: %s", response.MediaURL)
	}
}

func TestUploadPostMediaRejectsUnsupportedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	t.Cleanup(func() { loadActiveProfileViewer = originalLoader })
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}

	body, contentType := multipartImageRequestBody(t, "post.txt", []byte("not an image"))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/post-media", body)
	ctx.Request.Header.Set("Content-Type", contentType)
	ctx.Set("user_id", uint(42))

	UploadPostMedia(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestUploadPostMediaAcceptsOnlySupportedImageBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
	})
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error { return nil }

	cases := []struct {
		name    string
		payload []byte
		want    int
	}{
		{name: "empty", payload: nil, want: http.StatusBadRequest},
		{name: "jpeg bytes", payload: []byte{0xff, 0xd8, 0xff, 0xe0, 'j', 'p', 'e', 'g'}, want: http.StatusOK},
		{name: "png bytes", payload: append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("png")...), want: http.StatusOK},
		{name: "webp bytes", payload: append([]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}, []byte("webp")...), want: http.StatusOK},
		{name: "gif", payload: []byte("GIF89a"), want: http.StatusBadRequest},
		{name: "text", payload: []byte("plain text"), want: http.StatusBadRequest},
		{name: "too large", payload: make([]byte, maxPostMediaImageSize+1), want: http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, contentType := multipartImageRequestBody(t, "image.jpg", testCase.payload)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/post-media", body)
			ctx.Request.Header.Set("Content-Type", contentType)
			ctx.Set("user_id", uint(42))
			UploadPostMedia(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, testCase.want, recorder.Body.String())
			}
		})
	}
}

func multipartImageRequestBody(t *testing.T, filename string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, writer.FormDataContentType()
}
func TestUploadProfileAvatarStoresAllowedFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	originalStat := statProfileAvatarObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
		statProfileAvatarObject = originalStat
	})
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}

	cases := []struct {
		name     string
		filename string
		payload  []byte
		ext      string
		mime     string
	}{
		{name: "jpeg", filename: "avatar.jpg", payload: profileJPEGFixture(t), ext: ".jpg", mime: "image/jpeg"},
		{name: "png", filename: "avatar.png", payload: profilePNGFixture(t), ext: ".jpg", mime: "image/jpeg"},
		{name: "webp", filename: "avatar.webp", payload: profileWebPFixture(t), ext: ".jpg", mime: "image/jpeg"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotKey, gotMime string
			var gotSize int64
			var gotPayload []byte
			statProfileAvatarObject = func(context.Context, string) (storedObjectInfo, bool, error) {
				return storedObjectInfo{}, false, nil
			}
			putStoredObject = func(_ context.Context, key string, reader io.Reader, size int64, mime string) error {
				gotKey = key
				gotMime = mime
				gotSize = size
				var err error
				gotPayload, err = io.ReadAll(reader)
				return err
			}
			body, contentType := multipartImageRequestBody(t, testCase.filename, testCase.payload)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", body)
			ctx.Request.Header.Set("Content-Type", contentType)
			ctx.Set("user_id", uint(42))

			UploadProfileAvatar(ctx)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.HasPrefix(gotKey, "profile-avatars/users/v1/42/") || !strings.HasSuffix(gotKey, testCase.ext) {
				t.Fatalf("unexpected object key: %s", gotKey)
			}
			derivative, err := avatarimage.Optimize(testCase.payload)
			if err != nil {
				t.Fatal(err)
			}
			if gotMime != testCase.mime || gotSize != int64(len(derivative.Body)) || !bytes.Equal(gotPayload, derivative.Body) || bytes.Equal(gotPayload, testCase.payload) {
				t.Fatalf("unexpected stored object mime=%s size=%d payload=%v", gotMime, gotSize, gotPayload)
			}
			expectedKey, err := profileavatar.BuildUserV1ObjectKey(42, derivative.ContentHash, derivative.Extension)
			if err != nil || gotKey != expectedKey {
				t.Fatalf("unexpected content-addressed key=%q want=%q err=%v", gotKey, expectedKey, err)
			}
			var response map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(response["avatar_url"], "/api/files/profile-avatars/users/v1/42/") {
				t.Fatalf("unexpected avatar URL: %s", response["avatar_url"])
			}
		})
	}
}

func TestUploadProfileAvatarReusesExactDerivative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	originalStat := statProfileAvatarObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
		statProfileAvatarObject = originalStat
	})
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	payload := profileJPEGFixture(t)
	derivative, err := avatarimage.Optimize(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, err := profileavatar.BuildUserV1ObjectKey(42, derivative.ContentHash, derivative.Extension)
	if err != nil {
		t.Fatal(err)
	}
	statProfileAvatarObject = func(_ context.Context, gotKey string) (storedObjectInfo, bool, error) {
		if gotKey != key {
			t.Fatalf("stat key=%q want=%q", gotKey, key)
		}
		return storedObjectInfo{Size: int64(len(derivative.Body)), ContentType: derivative.ContentType}, true, nil
	}
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error {
		t.Fatal("exact derivative should be reused")
		return nil
	}
	body, contentType := multipartImageRequestBody(t, "avatar.jpg", payload)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", body)
	ctx.Request.Header.Set("Content-Type", contentType)
	ctx.Set("user_id", uint(42))
	UploadProfileAvatar(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUploadProfileAvatarReportsStorageUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	originalStat := statProfileAvatarObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
		statProfileAvatarObject = originalStat
	})
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	statProfileAvatarObject = func(context.Context, string) (storedObjectInfo, bool, error) {
		return storedObjectInfo{}, false, errors.New("storage unavailable")
	}
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error {
		t.Fatal("Put should not run when Stat fails")
		return nil
	}
	body, contentType := multipartImageRequestBody(t, "avatar.jpg", profileJPEGFixture(t))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", body)
	ctx.Request.Header.Set("Content-Type", contentType)
	ctx.Set("user_id", uint(42))
	UploadProfileAvatar(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUploadProfileAvatarRejectsInvalidInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	originalPut := putStoredObject
	t.Cleanup(func() {
		loadActiveProfileViewer = originalLoader
		putStoredObject = originalPut
	})
	loadActiveProfileViewer = func(uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error {
		t.Fatal("storage should not be called for invalid input")
		return nil
	}

	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "unsupported", payload: []byte("not an image")},
		{name: "too large", payload: make([]byte, maxProfileAvatarImageSize+1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, contentType := multipartImageRequestBody(t, "avatar.bin", testCase.payload)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", body)
			ctx.Request.Header.Set("Content-Type", contentType)
			ctx.Set("user_id", uint(42))
			UploadProfileAvatar(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", nil)
	ctx.Set("user_id", uint(42))
	UploadProfileAvatar(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing image status=%d", recorder.Code)
	}
}
func TestUploadProfileAvatarRequiresActiveViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLoader := loadActiveProfileViewer
	t.Cleanup(func() { loadActiveProfileViewer = originalLoader })

	loadActiveProfileViewer = func(uint) (models.User, error) {
		t.Fatal("active lookup should not run without context identity")
		return models.User{}, nil
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", nil)
	UploadProfileAvatar(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status=%d", recorder.Code)
	}

	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing viewer row", err: gorm.ErrRecordNotFound, want: http.StatusUnauthorized},
		{name: "unexpected lookup failure", err: errors.New("db unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			loadActiveProfileViewer = func(uint) (models.User, error) {
				return models.User{}, testCase.err
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/profile-avatar", nil)
			ctx.Set("user_id", uint(42))
			UploadProfileAvatar(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, testCase.want, recorder.Body.String())
			}
		})
	}
}

func TestFileObjectKeyAllowlistAcceptsNestedDevDataAvatarAndRejectsUnsafeKeys(t *testing.T) {
	for _, valid := range []string{
		"profile-avatars/devdata/mkbhd/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.jpg",
		"profile-avatars/users/v1/42/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.png",
		"profile-avatars/devdata/v1/mkbhd/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.jpg",
	} {
		if !isAllowedObjectKey(valid) {
			t.Fatalf("valid avatar key was rejected: %s", valid)
		}
	}
	for _, objectKey := range []string{
		"profile-avatars/devdata/mkbhd/../avatar.jpg",
		"profile-avatars/devdata/mkbhd/avatar\r\n.jpg",
		"article-covers/../avatar.jpg",
		"article-covers/avatar.jpg",
		"private/devdata/mkbhd/avatar.jpg",
	} {
		if isAllowedObjectKey(objectKey) {
			t.Fatalf("unsafe object key was accepted: %q", objectKey)
		}
	}
}

func TestFileCacheControlOnlyMakesV1AvatarsImmutable(t *testing.T) {
	for _, testCase := range []struct {
		key  string
		want string
	}{
		{key: "profile-avatars/users/v1/42/" + strings.Repeat("a", 64) + ".jpg", want: "public, max-age=31536000, immutable"},
		{key: "profile-avatars/devdata/v1/mkbhd/" + strings.Repeat("b", 64) + ".png", want: "public, max-age=31536000, immutable"},
		{key: "profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.jpg", want: "public, max-age=86400"},
		{key: "post-media/42/image.jpg", want: "public, max-age=86400"},
	} {
		if got := fileCacheControl(testCase.key); got != testCase.want {
			t.Fatalf("fileCacheControl(%q)=%q want=%q", testCase.key, got, testCase.want)
		}
	}
}

func profileJPEGFixture(t *testing.T) []byte {
	t.Helper()
	data := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			data.SetRGBA(x, y, color.RGBA{R: 220, G: 80, B: 40, A: 255})
		}
	}
	var body bytes.Buffer
	if err := jpeg.Encode(&body, data, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func profilePNGFixture(t *testing.T) []byte {
	t.Helper()
	data := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			data.SetNRGBA(x, y, color.NRGBA{R: 40, G: 180, B: 220, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, data); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func profileWebPFixture(t *testing.T) []byte {
	t.Helper()
	body, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	return body
}
