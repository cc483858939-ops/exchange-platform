package controllers

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/postmedia"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type mediaFileTransport struct{ calls int }

func (transport *mediaFileTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	headers := http.Header{"Content-Type": {"image/jpeg"}, "Content-Length": {"5"}, "Etag": {`"media-hash"`}, "Last-Modified": {"Sat, 03 Oct 2026 00:00:00 GMT"}}
	body := "image"
	if request.Method == http.MethodHead {
		body = ""
	}
	return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: 5, Request: request}, nil
}

func mediaFileRequest(key, conditional string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "objectKey", Value: "/" + key}}
	ctx.Request = httptest.NewRequest(http.MethodGet, postmedia.PublicURL(key), nil)
	ctx.Request.Header.Set("If-None-Match", conditional)
	GetFile(ctx)
	ctx.Writer.WriteHeaderNow()
	return recorder
}

func TestPostMediaEveryReadRevalidatesEligibilityBeforeStorageOrNotModified(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousGet := global.APIDb, getStoredObject
	t.Cleanup(func() { global.APIDb, getStoredObject = previousDB, previousGet })
	visible, reads := true, 0
	global.APIDb = visibilityRefreshDB(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		reads++
		for _, predicate := range []string{"media.url =", "posts.deleted_at IS NULL", "posts.visibility = 'public'", "post_author.deleted_at IS NULL"} {
			if !strings.Contains(query, predicate) {
				t.Fatalf("missing eligibility predicate %s: %s", predicate, query)
			}
		}
		if len(args) != 1 || !strings.HasPrefix(args[0].Value.(string), postmedia.FilesURLPrefix) {
			t.Fatalf("args=%v", args)
		}
		return &visibilityRefreshRows{columns: []string{"exists"}, values: [][]driver.Value{{visible}}}, nil
	})
	transport := &mediaFileTransport{}
	client, err := minio.New("media.invalid", &minio.Options{Creds: credentials.NewStaticV4("test", "test", ""), Region: "us-east-1", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	getStoredObject = func(ctx context.Context, key string) (*minio.Object, error) {
		return client.GetObject(ctx, "test-bucket", key, minio.GetObjectOptions{})
	}
	key := "post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/medium.jpg"
	first := mediaFileRequest(key, "")
	if first.Code != 200 || first.Body.String() != "image" || first.Header().Get("ETag") != `"media-hash"` {
		t.Fatalf("first=%d headers=%v body=%s", first.Code, first.Header(), first.Body.String())
	}
	for _, condition := range []string{`"media-hash"`, `"old", W/"media-hash"`, "*"} {
		cached := mediaFileRequest(key, condition)
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatalf("conditional=%s status=%d body=%s", condition, cached.Code, cached.Body.String())
		}
	}
	visible = false // deletion has committed; old client validators remain.
	before := transport.calls
	deleted := mediaFileRequest(key, `"media-hash"`)
	if deleted.Code != 404 || transport.calls != before || reads != 5 {
		t.Fatalf("deleted=%d storage=%d/%d eligibility=%d", deleted.Code, transport.calls, before, reads)
	}
	if deleted.Header().Get("Cache-Control") != "private, no-cache, max-age=0, must-revalidate" {
		t.Fatalf("deleted cache=%v", deleted.Header())
	}
}

func TestPostMediaEligibilityQueriesExactVariantAndFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousGet := global.APIDb, getStoredObject
	t.Cleanup(func() { global.APIDb, getStoredObject = previousDB, previousGet })
	getStoredObject = func(context.Context, string) (*minio.Object, error) {
		t.Fatal("ineligible media reached storage")
		return nil, nil
	}
	for _, variant := range []string{"medium.jpg", "large.png"} {
		for _, namespace := range []string{"post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/", "post-media/devdata/v1/test/12/" + strings.Repeat("a", 64) + "/"} {
			global.APIDb = visibilityRefreshDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
				column := "media.url ="
				if strings.HasPrefix(variant, "large") {
					column = "media.large_url ="
				}
				if !strings.Contains(query, column) || len(args) != 1 || args[0].Value != postmedia.PublicURL(namespace+variant) {
					t.Fatalf("query=%s args=%v", query, args)
				}
				return &visibilityRefreshRows{columns: []string{"exists"}, values: [][]driver.Value{{false}}}, nil
			})
			if got := mediaFileRequest(namespace+variant, "").Code; got != 404 {
				t.Fatalf("status=%d", got)
			}
		}
	}
	for _, failure := range []error{errors.New("database unavailable"), context.DeadlineExceeded, context.Canceled} {
		global.APIDb = visibilityRefreshDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) { return nil, failure })
		response := mediaFileRequest("post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/medium.jpg", `"media-hash"`)
		if response.Code == 200 || response.Code == 304 || response.Code == 404 {
			t.Fatalf("database failure %v returned %d", failure, response.Code)
		}
	}
	global.APIDb = nil
	if got := mediaFileRequest("post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/original.jpg", "").Code; got != 400 {
		t.Fatalf("original=%d", got)
	}
}
