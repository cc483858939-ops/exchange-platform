package controllers

import (
	"Go.exchange/consts"
	"Go.exchange/models"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func stubCreateArticleAuthor(t *testing.T) {
	original := loadArticleAuthorForCreate
	t.Cleanup(func() { loadArticleAuthorForCreate = original })
	loadArticleAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "alice", DisplayName: "Alice Chen", AvatarURL: "/api/files/profile-avatars/7/avatar.jpg"}, nil
	}
}

func stubArticleCreatePersistence(t *testing.T, persisted *models.Article, id uint) {
	original := createArticleWithEmbeddingJob
	t.Cleanup(func() { createArticleWithEmbeddingJob = original })
	createArticleWithEmbeddingJob = func(article *models.Article) error {
		article.ID = id
		if persisted != nil {
			*persisted = *article
		}
		return nil
	}
}

func TestCreateArticleBuildsPublishedRecordForEmbeddingJob(t *testing.T) {
	stubCreateArticleAuthor(t)
	stubArticleCreatePersistence(t, nil, 42)
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response articleResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 42 || response.PublicationState != consts.ArticlePublicationStatePublished || response.Title != "" || response.Content != "c" {
		t.Fatalf("response=%#v", response)
	}
}

func TestCreateArticleTrimsTextFields(t *testing.T) {
	stubCreateArticleAuthor(t)
	var persisted models.Article
	stubArticleCreatePersistence(t, &persisted, 43)
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"title\":\"  title  \",\"content\":\"  canonical body  \",\"preview\":\"  summary  \"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated || persisted.Title != "title" || persisted.Content != "canonical body" || persisted.Preview != "summary" {
		t.Fatalf("status=%d article=%#v", recorder.Code, persisted)
	}
}

func TestCreateArticleDoesNotPersistInvalidCover(t *testing.T) {
	stubCreateArticleAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := createArticleWithEmbeddingJob
	t.Cleanup(func() { createArticleWithEmbeddingJob = originalCreate })
	called := false
	createArticleWithEmbeddingJob = func(*models.Article) error { called = true; return errors.New("must not persist") }

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\"c\",\"cover_image_url\":\"https://invalid\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)

	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t", recorder.Code, called)
	}
}

func TestCreateArticlePersistsWithoutCover(t *testing.T) {
	stubCreateArticleAuthor(t)
	stubArticleCreatePersistence(t, nil, 42)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)
	if recorder.Code != http.StatusCreated || !bytes.Contains(recorder.Body.Bytes(), []byte("\"cover_image_url\":\"\"")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateArticleRejectsWhitespaceOnlyContent(t *testing.T) {
	stubCreateArticleAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := createArticleWithEmbeddingJob
	t.Cleanup(func() { createArticleWithEmbeddingJob = originalCreate })
	called := false
	createArticleWithEmbeddingJob = func(*models.Article) error { called = true; return errors.New("must not persist") }
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\" \\t\\n \"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestCreateArticleRejectsMissingUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\"c\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateArticleIgnoresSpoofedAuthorAndReturnsPublicAuthor(t *testing.T) {
	stubCreateArticleAuthor(t)
	var persisted models.Article
	stubArticleCreatePersistence(t, &persisted, 42)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"content\":\"c\",\"author_id\":999,\"author\":{\"id\":999}}"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	CreateArticle(ctx)
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
