package controllers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/consts"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

func stubCreateArticleAuthor(t *testing.T) {
	original := loadArticleAuthorForCreate
	t.Cleanup(func() { loadArticleAuthorForCreate = original })
	loadArticleAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "alice"}, nil
	}
}

func TestCreateArticleBuildsPublishedPendingAnalysisRecord(t *testing.T) {
	stubCreateArticleAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := createArticleWithAnalysisJob
	originalInvalidate := invalidateArticleListCache
	defer func() {
		createArticleWithAnalysisJob = originalCreate
		invalidateArticleListCache = originalInvalidate
	}()

	var persisted models.Article
	createArticleWithAnalysisJob = func(article *models.Article) error {
		article.ID = 42
		persisted = *article
		return nil
	}
	invalidateArticleListCache = func() error { return nil }

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p","cover_image_url":"/api/files/article-covers/cover.png"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if persisted.PublicationState != consts.ArticlePublicationStatePublished {
		t.Fatalf("publication state=%q", persisted.PublicationState)
	}
	if persisted.AnalysisState != consts.ArticleAnalysisStatePending {
		t.Fatalf("analysis state=%q", persisted.AnalysisState)
	}
	if persisted.AnalysisVersion != consts.ArticleAnalysisVersionV1 || persisted.PublishedAt == nil {
		t.Fatalf("expected clean-cut analysis metadata, got %#v", persisted)
	}
	if persisted.CoverImageURL != "/api/files/article-covers/cover.png" {
		t.Fatalf("cover image URL=%q", persisted.CoverImageURL)
	}
}

func TestCreateArticleDoesNotPersistInvalidCover(t *testing.T) {
	stubCreateArticleAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := createArticleWithAnalysisJob
	defer func() { createArticleWithAnalysisJob = originalCreate }()

	called := false
	createArticleWithAnalysisJob = func(*models.Article) error {
		called = true
		return errors.New("must not persist")
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p","cover_image_url":"https://invalid"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t", recorder.Code, called)
	}
}
func TestCreateArticlePersistsWithoutCover(t *testing.T) {
	stubCreateArticleAuthor(t)
	gin.SetMode(gin.TestMode)
	originalCreate := createArticleWithAnalysisJob
	originalInvalidate := invalidateArticleListCache
	defer func() {
		createArticleWithAnalysisJob = originalCreate
		invalidateArticleListCache = originalInvalidate
	}()

	var persisted models.Article
	createArticleWithAnalysisJob = func(article *models.Article) error {
		article.ID = 42
		persisted = *article
		return nil
	}
	invalidateArticleListCache = func() error { return nil }

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString("{\"title\":\"t\",\"content\":\"c\",\"preview\":\"p\"}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if persisted.CoverImageURL != "" {
		t.Fatalf("cover image URL=%q", persisted.CoverImageURL)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("\"cover_image_url\":\"\"")) {
		t.Fatalf("response missing empty cover image URL: %s", recorder.Body.String())
	}
}
func TestCreateArticleRejectsMissingUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p","cover_image_url":"/api/files/article-covers/cover.png"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateArticleIgnoresSpoofedAuthorAndReturnsPublicAuthor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stubCreateArticleAuthor(t)
	originalCreate := createArticleWithAnalysisJob
	t.Cleanup(func() { createArticleWithAnalysisJob = originalCreate })
	originalInvalidate := invalidateArticleListCache
	t.Cleanup(func() { invalidateArticleListCache = originalInvalidate })
	invalidateArticleListCache = func() error { return nil }
	var persisted models.Article
	createArticleWithAnalysisJob = func(article *models.Article) error {
		article.ID = 42
		persisted = *article
		return nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p","cover_image_url":"/api/files/article-covers/cover.png","author_id":999,"author":{"id":999}}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated || persisted.AuthorID != 7 {
		t.Fatalf("status=%d author_id=%d body=%s", recorder.Code, persisted.AuthorID, recorder.Body.String())
	}
	for _, forbidden := range []string{`"AuthorID"`, `"Password"`, `"DeletedAt"`, `"refresh_token"`} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("response leaked %s: %s", forbidden, recorder.Body.String())
		}
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"author":{"id":7,"username":"alice"}`)) {
		t.Fatalf("missing public author: %s", recorder.Body.String())
	}
}
