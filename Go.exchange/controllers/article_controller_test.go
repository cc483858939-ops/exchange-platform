package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/consts"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

func TestCreateArticleIgnoresAISystemFieldsAndQueuesPending(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalCreateArticleRecord := createArticleRecord
	originalInvalidateArticleListCache := invalidateArticleListCache
	originalEnqueueArticleAnalysis := enqueueArticleAnalysis
	originalMarkArticleStatus := markArticleStatus
	originalArticleControllerLogError := articleControllerLogError
	defer func() {
		createArticleRecord = originalCreateArticleRecord
		invalidateArticleListCache = originalInvalidateArticleListCache
		enqueueArticleAnalysis = originalEnqueueArticleAnalysis
		markArticleStatus = originalMarkArticleStatus
		articleControllerLogError = originalArticleControllerLogError
	}()

	persisted := models.Article{}
	createArticleRecord = func(article *models.Article) error {
		article.ID = 42
		persisted = *article
		return nil
	}
	cacheInvalidated := false
	invalidateArticleListCache = func() error {
		cacheInvalidated = true
		return nil
	}
	queuedID := uint(0)
	enqueueArticleAnalysis = func(articleID uint) error {
		queuedID = articleID
		return nil
	}
	markArticleStatus = func(id uint, status string) error {
		return nil
	}
	articleControllerLogError = func(*gin.Context, string, error) {}

	body := map[string]any{
		"title":    "hello",
		"content":  "world",
		"preview":  "preview",
		"summary":  "malicious",
		"tags":     []string{"x"},
		"category": "hack",
		"status":   "completed",
	}
	payload, _ := json.Marshal(body)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusCreated)
	}
	if !cacheInvalidated {
		t.Fatal("expected article list cache invalidation")
	}
	if queuedID != 42 {
		t.Fatalf("unexpected queued article id: %d", queuedID)
	}
	if persisted.Status != consts.ArticleStatusPending {
		t.Fatalf("unexpected persisted status: %s", persisted.Status)
	}
	if persisted.Summary != "" || persisted.Category != "" || len(persisted.Tags) != 0 {
		t.Fatalf("unexpected AI fields in persisted article: %#v", persisted)
	}
}

func TestCreateArticleMarksFailedWhenEnqueueFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalCreateArticleRecord := createArticleRecord
	originalInvalidateArticleListCache := invalidateArticleListCache
	originalEnqueueArticleAnalysis := enqueueArticleAnalysis
	originalMarkArticleStatus := markArticleStatus
	originalArticleControllerLogError := articleControllerLogError
	defer func() {
		createArticleRecord = originalCreateArticleRecord
		invalidateArticleListCache = originalInvalidateArticleListCache
		enqueueArticleAnalysis = originalEnqueueArticleAnalysis
		markArticleStatus = originalMarkArticleStatus
		articleControllerLogError = originalArticleControllerLogError
	}()

	createArticleRecord = func(article *models.Article) error {
		article.ID = 7
		return nil
	}
	invalidateArticleListCache = func() error {
		return nil
	}
	enqueueArticleAnalysis = func(articleID uint) error {
		return errors.New("redis down")
	}
	markedID := uint(0)
	markedStatus := ""
	markArticleStatus = func(id uint, status string) error {
		markedID = id
		markedStatus = status
		return nil
	}
	articleControllerLogError = func(*gin.Context, string, error) {}

	body := []byte(`{"title":"hello","content":"world","preview":"preview"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/articles", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticle(ctx)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusCreated)
	}
	if markedID != 7 || markedStatus != consts.ArticleStatusFailed {
		t.Fatalf("unexpected failed status update: id=%d status=%s", markedID, markedStatus)
	}
}
