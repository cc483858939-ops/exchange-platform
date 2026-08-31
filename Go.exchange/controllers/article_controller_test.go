package controllers

import (
	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/models"
	"bytes"
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
	persistPostGraphFn = func(post *models.Post, article **models.PostArticle, userID uint, content string, req createPostRequest, now time.Time) error {
		*post = models.Post{Model: gorm.Model{ID: id, CreatedAt: now, UpdatedAt: now}, AuthorID: userID, Content: content, Visibility: "public"}
		if req.Article != nil {
			*article = &models.PostArticle{PostID: id, Title: strings.TrimSpace(req.Article.Title), Preview: strings.TrimSpace(req.Article.Preview), CoverImageURL: strings.TrimSpace(req.Article.CoverImageURL), PublicationState: consts.PostPublicationStatePublished, PublishedAt: &now, ExpiredAt: req.Article.ExpiredAt}
		}
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
	if response.ID != 42 || response.PublishedAt == nil || response.Article != nil || response.Content != "c" {
		t.Fatalf("response=%#v", response)
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

func TestCreatePostDoesNotPersistInvalidCover(t *testing.T) {
	stubCreatePostAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := persistPostGraphFn
	t.Cleanup(func() { persistPostGraphFn = originalCreate })
	called := false
	persistPostGraphFn = func(*models.Post, **models.PostArticle, uint, string, createPostRequest, time.Time) error {
		called = true
		return errors.New("must not persist")
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString("{\"content\":\"c\",\"article\":{\"title\":\"title\",\"preview\":\"preview\",\"cover_image_url\":\"https://invalid\"}}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)

	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t", recorder.Code, called)
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
	if recorder.Code != http.StatusCreated || !bytes.Contains(recorder.Body.Bytes(), []byte("\"article\":null")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostRejectsWhitespaceOnlyContent(t *testing.T) {
	stubCreatePostAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := persistPostGraphFn
	t.Cleanup(func() { persistPostGraphFn = originalCreate })
	called := false
	persistPostGraphFn = func(*models.Post, **models.PostArticle, uint, string, createPostRequest, time.Time) error {
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
