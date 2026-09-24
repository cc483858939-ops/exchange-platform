package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newTopicControllerContext(path, slug string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Params = gin.Params{{Key: "slug", Value: slug}}
	return ctx, recorder
}

func topicTestCatalog() config.CuratedTopicsConfig {
	return config.CuratedTopicsConfig{
		Version: config.CuratedTopicsVersion,
		Topics: []config.CuratedTopic{
			{Slug: "topic-one", Label: "Topic One", Description: "A test topic", SourceKeys: []string{"included", "disabled"}, Enabled: true},
			{Slug: "hidden", Label: "Hidden", Description: "Disabled topic", SourceKeys: []string{"other"}, Enabled: false},
		},
	}
}

func TestGetTopicsReturnsEnabledSummariesInConfigOrder(t *testing.T) {
	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	ctx, recorder := newTopicControllerContext("/api/topics", "")
	GetTopics(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "source_keys") {
		t.Fatalf("public topic list leaked source keys: %s", recorder.Body.String())
	}
	var response topicListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0] != (topicSummaryResponse{Slug: "topic-one", Label: "Topic One", Description: "A test topic"}) {
		t.Fatalf("items=%+v", response.Items)
	}
}

func TestGetTopicsHidesConfigurationErrors(t *testing.T) {
	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) {
		return config.CuratedTopicsConfig{}, errors.New("private config path")
	}
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	ctx, recorder := newTopicControllerContext("/api/topics", "")
	GetTopics(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private config path") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetTopicPostsParsesLimitsAndReturnsEmptyArray(t *testing.T) {
	originalConfigLoader, originalPageLoader := loadTopicConfiguration, loadTopicPostsPage
	originalDB := global.Db
	global.Db = &gorm.DB{}
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	var gotLimits []int
	loadTopicPostsPage = func(_ config.CuratedTopic, limit int, cursor *topicPostCursor) (postPageResponse, error) {
		if cursor != nil {
			t.Fatalf("unexpected cursor: %+v", cursor)
		}
		gotLimits = append(gotLimits, limit)
		return postPageResponse{Items: make([]postResponse, 0)}, nil
	}
	t.Cleanup(func() {
		loadTopicConfiguration, loadTopicPostsPage, global.Db = originalConfigLoader, originalPageLoader, originalDB
	})

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/topics/topic-one/posts", want: defaultTopicPostLimit},
		{path: "/api/topics/topic-one/posts?limit=100", want: maxTopicPostLimit},
	} {
		ctx, recorder := newTopicControllerContext(test.path, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items":[]`) || !strings.Contains(recorder.Body.String(), `"next_cursor":null`) {
			t.Fatalf("path=%q status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
		if got := gotLimits[len(gotLimits)-1]; got != test.want {
			t.Fatalf("path=%q limit=%d, want %d", test.path, got, test.want)
		}
	}
}

func TestGetTopicPostsRejectsBadQueriesAndUnknownTopics(t *testing.T) {
	originalConfigLoader, originalPageLoader := loadTopicConfiguration, loadTopicPostsPage
	originalDB := global.Db
	global.Db = &gorm.DB{}
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	loadTopicPostsPage = func(config.CuratedTopic, int, *topicPostCursor) (postPageResponse, error) {
		t.Fatal("page loader should not run for invalid input")
		return postPageResponse{}, nil
	}
	t.Cleanup(func() {
		loadTopicConfiguration, loadTopicPostsPage, global.Db = originalConfigLoader, originalPageLoader, originalDB
	})

	for _, rawLimit := range []string{"0", "-1", "bad"} {
		ctx, recorder := newTopicControllerContext("/api/topics/topic-one/posts?limit="+rawLimit, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", rawLimit, recorder.Code, recorder.Body.String())
		}
	}
	for _, rawCursor := range []string{"bad", base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-01-01T00:00:00Z"}`))} {
		ctx, recorder := newTopicControllerContext("/api/topics/topic-one/posts?cursor="+rawCursor, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("cursor=%q status=%d body=%s", rawCursor, recorder.Code, recorder.Body.String())
		}
	}
	for _, slug := range []string{"unknown", "hidden"} {
		ctx, recorder := newTopicControllerContext("/api/topics/"+slug+"/posts", slug)
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "topic not found") {
			t.Fatalf("slug=%q status=%d body=%s", slug, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTopicCursorRequiresTimestampAndPostID(t *testing.T) {
	createdAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	want := topicPostCursor{CreatedAt: createdAt, PostID: 42}
	raw, err := encodeTopicPostCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeTopicPostCursor(raw)
	if err != nil || !got.CreatedAt.Equal(want.CreatedAt) || got.PostID != want.PostID {
		t.Fatalf("decoded cursor=%+v err=%v", got, err)
	}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "malformed base64", raw: "%%%"},
		{name: "missing created_at", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"post_id":42}`))},
		{name: "missing post_id", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-09-20T12:00:00Z"}`))},
		{name: "zero post_id", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-09-20T12:00:00Z","post_id":0}`))},
		{name: "unknown field", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-09-20T12:00:00Z","post_id":42,"extra":true}`))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeTopicPostCursor(test.raw); err == nil {
				t.Fatalf("cursor %q was accepted", test.raw)
			}
		})
	}
}
