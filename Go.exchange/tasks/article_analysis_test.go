package tasks

import (
	"context"
	"sync"
	"testing"

	"Go.exchange/consts"
	"Go.exchange/models"
)

type stubArticleAnalyzer struct {
	result ArticleAnalysisResult
	err    error
}

func (s stubArticleAnalyzer) Analyze(context.Context, models.Article) (ArticleAnalysisResult, error) {
	return s.result, s.err
}

func TestProcessArticleAnalysisTaskSuccess(t *testing.T) {
	originalUpdateStatus := updateArticleAnalysisStatus
	originalLoadArticle := loadArticleForAnalysis
	originalSaveResult := saveArticleAnalysisResult
	originalAck := ackArticleAnalysisTask
	originalInvalidate := invalidateArticleDetailCache
	defer func() {
		updateArticleAnalysisStatus = originalUpdateStatus
		loadArticleForAnalysis = originalLoadArticle
		saveArticleAnalysisResult = originalSaveResult
		ackArticleAnalysisTask = originalAck
		invalidateArticleDetailCache = originalInvalidate
	}()

	statuses := make([]string, 0, 1)
	updateArticleAnalysisStatus = func(id uint, status string) error {
		statuses = append(statuses, status)
		return nil
	}
	loadArticleForAnalysis = func(id uint) (models.Article, error) {
		return models.Article{Title: "t", Preview: "p", Content: "c"}, nil
	}
	saved := ArticleAnalysisResult{}
	saveCalled := false
	saveArticleAnalysisResult = func(id uint, result ArticleAnalysisResult) error {
		saveCalled = true
		saved = result
		return nil
	}
	ackCalled := false
	ackArticleAnalysisTask = func(articleID uint) error {
		ackCalled = true
		return nil
	}
	invalidations := 0
	invalidateArticleDetailCache = func(id uint) error {
		invalidations++
		return nil
	}

	processArticleAnalysisTask(context.Background(), stubArticleAnalyzer{
		result: ArticleAnalysisResult{Summary: "summary", Tags: []string{"go", "ai", "async"}, Category: "tech"},
	}, 7)

	if len(statuses) != 1 || statuses[0] != consts.ArticleStatusProcessing {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
	if !saveCalled {
		t.Fatal("expected analysis result to be saved")
	}
	if saved.Category != "tech" || saved.Summary != "summary" || len(saved.Tags) != 3 {
		t.Fatalf("unexpected saved result: %#v", saved)
	}
	if !ackCalled {
		t.Fatal("expected task to be ACKed")
	}
	if invalidations != 2 {
		t.Fatalf("expected 2 cache invalidations, got %d", invalidations)
	}
}

func TestProcessArticleAnalysisTaskFailureMarksFailed(t *testing.T) {
	originalUpdateStatus := updateArticleAnalysisStatus
	originalLoadArticle := loadArticleForAnalysis
	originalAck := ackArticleAnalysisTask
	originalInvalidate := invalidateArticleDetailCache
	defer func() {
		updateArticleAnalysisStatus = originalUpdateStatus
		loadArticleForAnalysis = originalLoadArticle
		ackArticleAnalysisTask = originalAck
		invalidateArticleDetailCache = originalInvalidate
	}()

	statuses := make([]string, 0, 2)
	updateArticleAnalysisStatus = func(id uint, status string) error {
		statuses = append(statuses, status)
		return nil
	}
	loadArticleForAnalysis = func(id uint) (models.Article, error) {
		return models.Article{Title: "t", Preview: "p", Content: "c"}, nil
	}
	ackCalled := false
	ackArticleAnalysisTask = func(articleID uint) error {
		ackCalled = true
		return nil
	}
	invalidations := 0
	invalidateArticleDetailCache = func(id uint) error {
		invalidations++
		return nil
	}

	processArticleAnalysisTask(context.Background(), stubArticleAnalyzer{err: context.DeadlineExceeded}, 8)

	if len(statuses) != 2 || statuses[0] != consts.ArticleStatusProcessing || statuses[1] != consts.ArticleStatusFailed {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
	if !ackCalled {
		t.Fatal("expected failed task to be ACKed")
	}
	if invalidations != 2 {
		t.Fatalf("expected 2 cache invalidations, got %d", invalidations)
	}
}

func TestParseArticleAnalysisResponse(t *testing.T) {
	result, err := parseArticleAnalysisResponse("```json\n{\"summary\":\"brief\",\"tags\":[\"go\",\"ai\",\"async\"],\"category\":\"backend\"}\n```")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "brief" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if result.Category != "backend" {
		t.Fatalf("unexpected category: %q", result.Category)
	}
	if len(result.Tags) != 3 {
		t.Fatalf("unexpected tags: %#v", result.Tags)
	}
}

func TestArticleAnalysisLoopReturnsWhenAnalyzerInitFails(t *testing.T) {
	originalNewArticleAnalyzer := newArticleAnalyzer
	defer func() {
		newArticleAnalyzer = originalNewArticleAnalyzer
	}()

	newArticleAnalyzer = func() (ArticleAnalyzer, error) {
		return nil, context.Canceled
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go articleAnalysisLoop(ctx, &wg)
	wg.Wait()
}
