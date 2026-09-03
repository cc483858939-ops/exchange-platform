package controllers

import (
	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func stubCreatePostAuthor(t *testing.T) {
	original := loadPostAuthorForCreate
	t.Cleanup(func() { loadPostAuthorForCreate = original })
	loadPostAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "alice", DisplayName: "Alice Chen", AvatarURL: "/api/files/profile-avatars/7/avatar.jpg"}, nil
	}
}

func stubPostCreatePersistence(t *testing.T, persisted *models.Post, id uint) {
	original := persistPostGraphFn
	t.Cleanup(func() { persistPostGraphFn = original })
	persistPostGraphFn = func(post *models.Post, userID uint, content string, req createPostRequest, _ []validatedPostMedia, now time.Time) error {
		*post = models.Post{Model: gorm.Model{ID: id, CreatedAt: now, UpdatedAt: now}, AuthorID: userID, Content: content, Visibility: "public"}
		if persisted != nil {
			*persisted = *post
		}
		return nil
	}
}

func TestCreatePostBuildsPublishedRecord(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 42)
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	createPost(ctx, nil)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 42 || response.PublishedAt == nil || response.Content != "c" {
		t.Fatalf("response=%#v", response)
	}
	if response.Media == nil {
		t.Fatal("no-media create response must contain an empty media array")
	}
}

func TestCreatePostValidatesAndReturnsOrderedMedia(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 44)
	originalStat := statStoredObject
	t.Cleanup(func() { statStoredObject = originalStat })
	statStoredObject = func(context.Context, string) error { return nil }
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString(`{"content":"with media","media":[{"type":"image","url":"/api/files/post-media/7/550e8400-e29b-41d4-a716-446655440000.jpg"},{"type":"image","url":"/api/files/post-media/7/550e8400-e29b-41d4-a716-446655440001.webp"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Media) != 2 || response.Media[0].Position != 0 || response.Media[1].Position != 1 || response.Media[1].URL == response.Media[0].URL {
		t.Fatalf("media=%#v", response.Media)
	}
}

func TestCreatePostRejectsMoreThanFourMediaBeforePersistence(t *testing.T) {
	stubCreatePostAuthor(t)
	originalStat := statStoredObject
	originalPersist := persistPostGraphFn
	t.Cleanup(func() {
		statStoredObject = originalStat
		persistPostGraphFn = originalPersist
	})
	statStoredObject = func(context.Context, string) error { return nil }
	persistCalled := false
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		persistCalled = true
		return nil
	}
	items := strings.Repeat(`{"type":"image","url":"/api/files/post-media/7/550e8400-e29b-41d4-a716-446655440000.jpg"},`, 4) + `{"type":"image","url":"/api/files/post-media/7/550e8400-e29b-41d4-a716-446655440001.jpg"}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString(`{"content":"too many","media":[`+items+`]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusBadRequest || persistCalled {
		t.Fatalf("status=%d persistCalled=%t body=%s", recorder.Code, persistCalled, recorder.Body.String())
	}
}

func TestCreatePostTrimsTextFields(t *testing.T) {
	stubCreatePostAuthor(t)
	var persisted models.Post
	stubPostCreatePersistence(t, &persisted, 43)
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"  canonical body  \",\"article\":{\"title\":\"  title  \",\"preview\":\"  summary  \"}}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)

	if recorder.Code != http.StatusCreated || persisted.Content != "canonical body" {
		t.Fatalf("status=%d article=%#v", recorder.Code, persisted)
	}
}

func TestCreatePostPersistsWithoutCover(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 42)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated || bytes.Contains(recorder.Body.Bytes(), []byte("\"article\"")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostRejectsWhitespaceOnlyContent(t *testing.T) {
	stubCreatePostAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := persistPostGraphFn
	t.Cleanup(func() { persistPostGraphFn = originalCreate })
	called := false
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		called = true
		return errors.New("must not persist")
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\" \\t\\n \"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestCreatePostAcceptsReplyAtUnicodeRuneLimit(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 47)
	content := strings.Repeat("界", maxReplyContentRunes)
	parentID := uint(7)
	body, err := json.Marshal(createPostRequest{Content: content, ReplyToPostID: &parentID})
	if err != nil {
		t.Fatal(err)
	}

	recorder := executeCreatePostRequest(t, body, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostRejectsReplyAboveUnicodeRuneLimitBeforeSideEffects(t *testing.T) {
	stubCreatePostAuthor(t)
	originalPersist := persistPostGraphFn
	originalInitialize := initializePostLikeState
	originalInvalidate := invalidatePostCreateParentDetailCache
	originalConfig := config.AppConfig
	persistCalls := 0
	initializeCalls := 0
	invalidateCalls := 0
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		persistCalls++
		return nil
	}
	initializePostLikeState = func(uint) error {
		initializeCalls++
		return nil
	}
	invalidatePostCreateParentDetailCache = func(uint) error {
		invalidateCalls++
		return nil
	}
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: true}}
	publisher := &recommendationTestPublisher{}
	t.Cleanup(func() {
		persistPostGraphFn = originalPersist
		initializePostLikeState = originalInitialize
		invalidatePostCreateParentDetailCache = originalInvalidate
		config.AppConfig = originalConfig
	})

	parentID := uint(7)
	body, err := json.Marshal(createPostRequest{Content: strings.Repeat("界", maxReplyContentRunes+1), ReplyToPostID: &parentID})
	if err != nil {
		t.Fatal(err)
	}
	recorder := executeCreatePostRequest(t, body, publisher)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if persistCalls != 0 || publisher.calls != 0 || initializeCalls != 0 || invalidateCalls != 0 {
		t.Fatalf("side effects persist=%d publish=%d initialize=%d invalidate=%d", persistCalls, publisher.calls, initializeCalls, invalidateCalls)
	}
}

func TestCreatePostDoesNotApplyReplyRuneLimitToRoot(t *testing.T) {
	tests := []struct {
		name    string
		request createPostRequest
	}{
		{
			name:    "root",
			request: createPostRequest{Content: strings.Repeat("界", maxReplyContentRunes+1)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stubCreatePostAuthor(t)
			stubPostCreatePersistence(t, nil, 48)
			body, err := json.Marshal(test.request)
			if err != nil {
				t.Fatal(err)
			}
			recorder := executeCreatePostRequest(t, body, nil)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreatePostRejectsOversizedAndMalformedJSONBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantStatus int
	}{
		{
			name:       "oversized envelope",
			body:       mustMarshalCreatePostBody(t, strings.Repeat("a", createPostRequestMaxBytes)),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{name: "malformed json", body: []byte("{"), wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stubCreatePostAuthor(t)
			originalPersist := persistPostGraphFn
			originalInitialize := initializePostLikeState
			originalInvalidate := invalidatePostCreateParentDetailCache
			originalConfig := config.AppConfig
			persistCalls := 0
			initializeCalls := 0
			invalidateCalls := 0
			persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
				persistCalls++
				return nil
			}
			initializePostLikeState = func(uint) error {
				initializeCalls++
				return nil
			}
			invalidatePostCreateParentDetailCache = func(uint) error {
				invalidateCalls++
				return nil
			}
			config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: true}}
			publisher := &recommendationTestPublisher{}
			t.Cleanup(func() {
				persistPostGraphFn = originalPersist
				initializePostLikeState = originalInitialize
				invalidatePostCreateParentDetailCache = originalInvalidate
				config.AppConfig = originalConfig
			})

			recorder := executeCreatePostRequest(t, test.body, publisher)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
			if persistCalls != 0 || publisher.calls != 0 || initializeCalls != 0 || invalidateCalls != 0 {
				t.Fatalf("side effects persist=%d publish=%d initialize=%d invalidate=%d", persistCalls, publisher.calls, initializeCalls, invalidateCalls)
			}
		})
	}
}

func executeCreatePostRequest(t *testing.T, body []byte, publisher eventing.BatchPublisher) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	NewCreatePostHandler(publisher)(ctx)
	return recorder
}

func mustMarshalCreatePostBody(t *testing.T, content string) []byte {
	t.Helper()
	body, err := json.Marshal(createPostRequest{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCreatePostRejectsMissingUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostIgnoresSpoofedAuthorAndReturnsPublicAuthor(t *testing.T) {
	stubCreatePostAuthor(t)
	var persisted models.Post
	stubPostCreatePersistence(t, &persisted, 42)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\",\"author_id\":999,\"author\":{\"id\":999}}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated || persisted.AuthorID != 7 {
		t.Fatalf("status=%d author_id=%d body=%s", recorder.Code, persisted.AuthorID, recorder.Body.String())
	}
	for _, forbidden := range []string{`"AuthorID"`, `"Password"`, `"DeletedAt"`, `"Bio"`, `"bio"`, `"refresh_token"`} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("response leaked %s: %s", forbidden, recorder.Body.String())
		}
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"author":{"id":7,"username":"alice","display_name":"Alice Chen","avatar_url":"/api/files/profile-avatars/7/avatar.jpg"}`)) {
		t.Fatalf("missing public author: %s", recorder.Body.String())
	}
}

func TestCreatePostPublishesEmbeddingRequestAfterPersistence(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 44)
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: true, Version: "test-version"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	publisher := &recommendationTestPublisher{}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewCreatePostHandler(publisher)(ctx)

	if recorder.Code != http.StatusCreated || publisher.calls != 1 || len(publisher.events) != 1 {
		t.Fatalf("status=%d calls=%d events=%d body=%s", recorder.Code, publisher.calls, len(publisher.events), recorder.Body.String())
	}
	event := publisher.events[0]
	if event.Type != eventing.EventTypePostEmbeddingRequested || event.AggregateID != "44" || string(event.Payload) != "{\"post_id\":44}" {
		t.Fatalf("event=%#v", event)
	}
}

func TestCreatePostReturnsCreatedWhenEmbeddingPublishFails(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 45)
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: true}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewCreatePostHandler(publisher)(ctx)

	if recorder.Code != http.StatusCreated || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestCreatePostDoesNotPublishWhenEmbeddingDisabled(t *testing.T) {
	stubCreatePostAuthor(t)
	stubPostCreatePersistence(t, nil, 46)
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: false}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	publisher := &recommendationTestPublisher{}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	NewCreatePostHandler(publisher)(ctx)

	if recorder.Code != http.StatusCreated || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}
