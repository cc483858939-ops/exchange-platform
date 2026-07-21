package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUploadArticleCoverStoresImageAndReturnsURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalPutArticleCoverObject := putArticleCoverObject
	defer func() {
		putArticleCoverObject = originalPutArticleCoverObject
	}()

	pngPayload := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("payload")...)
	var gotObjectKey string
	var gotContentType string
	var gotObjectSize int64
	var gotPayload []byte
	putArticleCoverObject = func(ctx context.Context, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
		var err error
		gotObjectKey = objectKey
		gotContentType = contentType
		gotObjectSize = objectSize
		gotPayload, err = io.ReadAll(reader)
		return err
	}

	body, contentType := multipartImageRequestBody(t, "cover.png", pngPayload)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/article-cover", body)
	ctx.Request.Header.Set("Content-Type", contentType)

	UploadArticleCover(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(gotObjectKey, articleCoverObjectPrefix) || !strings.HasSuffix(gotObjectKey, ".png") {
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

	var response articleCoverUploadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(response.CoverImageURL, "/api/files/"+articleCoverObjectPrefix) {
		t.Fatalf("unexpected cover url: %s", response.CoverImageURL)
	}
}

func TestUploadArticleCoverRejectsUnsupportedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body, contentType := multipartImageRequestBody(t, "cover.txt", []byte("not an image"))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/article-cover", body)
	ctx.Request.Header.Set("Content-Type", contentType)

	UploadArticleCover(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusBadRequest)
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
