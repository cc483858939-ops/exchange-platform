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

func TestCreateArticleBuildsPublishedPendingAnalysisRecord(t *testing.T) {
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
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p"}`))
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
}

func TestCreateArticleDoesNotPersistInvalidCover(t *testing.T) {
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
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewBufferString(`{"title":"t","content":"c","preview":"p","cover_image_url":"https://invalid"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t", recorder.Code, called)
	}
}
