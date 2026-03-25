package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/cloudwego/eino/schema"
)

func TestNewEINOArticleAnalysisAgentAppliesConfigFallbacks(t *testing.T) {
	agent, err := NewEINOArticleAnalysisAgent(config.AIConfig{
		BaseURL: "https://example.com",
		APIKey:  "test-key",
		Model:   "shared-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agent.cfg.ChunkModel != "shared-model" {
		t.Fatalf("expected chunk model fallback, got %q", agent.cfg.ChunkModel)
	}
	if agent.cfg.MainModel != "shared-model" {
		t.Fatalf("expected main model fallback, got %q", agent.cfg.MainModel)
	}
	if agent.cfg.ChunkSize != defaultChunkSize {
		t.Fatalf("expected default chunk size %d, got %d", defaultChunkSize, agent.cfg.ChunkSize)
	}
	if agent.cfg.ChunkOverlap != 0 {
		t.Fatalf("expected default chunk overlap 0, got %d", agent.cfg.ChunkOverlap)
	}
	if agent.cfg.MaxChunkParallelism != defaultMaxChunkParallelism {
		t.Fatalf("expected default max chunk parallelism %d, got %d", defaultMaxChunkParallelism, agent.cfg.MaxChunkParallelism)
	}
	if agent.cfg.TopNTags != defaultTopNTags {
		t.Fatalf("expected default topN %d, got %d", defaultTopNTags, agent.cfg.TopNTags)
	}
	if agent.pipeline == nil {
		t.Fatal("expected analysis pipeline to be compiled")
	}
}

func TestNewEINOArticleAnalysisAgentUsesExplicitModelOverrides(t *testing.T) {
	agent, err := NewEINOArticleAnalysisAgent(config.AIConfig{
		BaseURL:             "https://example.com",
		APIKey:              "test-key",
		Model:               "shared-model",
		ChunkModel:          "chunk-model",
		MainModel:           "main-model",
		ChunkSize:           200,
		ChunkOverlap:        200,
		MaxChunkParallelism: 4,
		TopNTags:            8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agent.cfg.ChunkModel != "chunk-model" {
		t.Fatalf("expected explicit chunk model, got %q", agent.cfg.ChunkModel)
	}
	if agent.cfg.MainModel != "main-model" {
		t.Fatalf("expected explicit main model, got %q", agent.cfg.MainModel)
	}
	if agent.cfg.ChunkOverlap != 0 {
		t.Fatalf("expected invalid overlap to be reset, got %d", agent.cfg.ChunkOverlap)
	}
	if agent.cfg.MaxChunkParallelism != 4 {
		t.Fatalf("expected explicit chunk parallelism, got %d", agent.cfg.MaxChunkParallelism)
	}
	if agent.cfg.TopNTags != 8 {
		t.Fatalf("expected explicit topN, got %d", agent.cfg.TopNTags)
	}
}

func TestChunkArticleKeepsParagraphBoundaries(t *testing.T) {
	chunks := chunkArticle("para one\n\npara two", 100, 10)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Content != "para one" || chunks[1].Content != "para two" {
		t.Fatalf("unexpected paragraph chunks: %#v", chunks)
	}
}

func TestChunkArticleFallsBackToSingleNewlines(t *testing.T) {
	chunks := chunkArticle("line one\nline two", 100, 10)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Content != "line one" || chunks[1].Content != "line two" {
		t.Fatalf("unexpected newline chunks: %#v", chunks)
	}
}

func TestChunkArticleSplitsOversizedParagraphByPrimaryPunctuation(t *testing.T) {
	chunks := chunkArticle("aaaa。bbbb。cccc", 6, 1)
	want := []string{"aaaa。", "bbbb。", "cccc"}
	assertChunkContents(t, chunks, want)
}

func TestChunkArticleSplitsOversizedParagraphBySecondaryPunctuation(t *testing.T) {
	chunks := chunkArticle("aaaa,bbbb,cccc", 6, 1)
	want := []string{"aaaa,", "bbbb,", "cccc"}
	assertChunkContents(t, chunks, want)
}

func TestChunkArticleHardSplitsWhenNoPunctuationExists(t *testing.T) {
	chunks := chunkArticle("abcdefghij", 4, 1)
	want := []string{"abcd", "defg", "ghij"}
	assertChunkContents(t, chunks, want)
}

func TestChunkArticleDisablesInvalidOverlapForHardSplit(t *testing.T) {
	chunks := chunkArticle("abcdefghij", 4, 4)
	want := []string{"abcd", "efgh", "ij"}
	assertChunkContents(t, chunks, want)
}

func TestAggregateChunksNormalizesTagsAndCategories(t *testing.T) {
	aggregated := aggregateChunks([]ChunkAnalysisResult{
		{ChunkIndex: 1, Summary: "second", Tags: []string{"Go", "AI"}, Category: "Tech"},
		{ChunkIndex: 0, Summary: "first", Tags: []string{"go", "Backend"}, Category: "tech"},
		{ChunkIndex: 2, Summary: "third", Tags: []string{"AI", "Go"}, Category: "News"},
	}, 1)

	if aggregated.SuccessChunks != 3 || aggregated.FailedChunks != 1 {
		t.Fatalf("unexpected chunk stats: %#v", aggregated)
	}

	wantSummaries := []string{"first", "second", "third"}
	for i := range wantSummaries {
		if aggregated.ChunkSummaries[i] != wantSummaries[i] {
			t.Fatalf("unexpected summary order: got %#v want %#v", aggregated.ChunkSummaries, wantSummaries)
		}
	}

	if len(aggregated.TagCandidates) != 3 {
		t.Fatalf("unexpected tag candidates: %#v", aggregated.TagCandidates)
	}
	if aggregated.TagCandidates[0].Tag != "go" || aggregated.TagCandidates[0].Count != 3 {
		t.Fatalf("unexpected primary tag candidate: %#v", aggregated.TagCandidates[0])
	}
	if aggregated.TagCandidates[1].Tag != "AI" || aggregated.TagCandidates[1].Count != 2 {
		t.Fatalf("unexpected secondary tag candidate: %#v", aggregated.TagCandidates[1])
	}
	if aggregated.CategoryCounts[0].Category != "tech" || aggregated.CategoryCounts[0].Count != 2 {
		t.Fatalf("unexpected category counts: %#v", aggregated.CategoryCounts)
	}
}

func TestParseFinalArticleAnalysisResponseFiltersAndTruncatesTags(t *testing.T) {
	result, err := parseFinalArticleAnalysisResponse(`{"summary":"brief","tags":["AI","unknown","Go","AI"],"category":"backend"}`, []TagCandidate{{Tag: "Go", Count: 3}, {Tag: "AI", Count: 2}}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Tags) != 1 || result.Tags[0] != "AI" {
		t.Fatalf("unexpected filtered tags: %#v", result.Tags)
	}
}

func TestAnalyzeUsesBoundedParallelFanOutAndOrderedAggregation(t *testing.T) {
	cfg := normalizeAIConfig(config.AIConfig{
		BaseURL:             "https://example.com",
		APIKey:              "test-key",
		ChunkModel:          "chunk-model",
		MainModel:           "main-model",
		ChunkSize:           100,
		MaxChunkParallelism: 2,
		TopNTags:            2,
	})

	var mu sync.Mutex
	activeCalls := 0
	maxActiveCalls := 0
	mainPrompt := ""

	agent := &EINOArticleAnalysisAgent{
		cfg: cfg,
		callModel: func(ctx context.Context, modelName string, messages []*schema.Message) (string, error) {
			prompt := fmt.Sprint(messages[len(messages)-1].Content)
			switch modelName {
			case "chunk-model":
				mu.Lock()
				activeCalls++
				if activeCalls > maxActiveCalls {
					maxActiveCalls = activeCalls
				}
				mu.Unlock()

				defer func() {
					mu.Lock()
					activeCalls--
					mu.Unlock()
				}()

				switch {
				case strings.Contains(prompt, `"content": "chunk-one"`):
					time.Sleep(50 * time.Millisecond)
					return `{"summary":"summary-1","tags":["Tag-1"],"category":"Cat-1"}`, nil
				case strings.Contains(prompt, `"content": "chunk-two"`):
					time.Sleep(10 * time.Millisecond)
					return `{"summary":"summary-2","tags":["Tag-2"],"category":"Cat-2"}`, nil
				case strings.Contains(prompt, `"content": "chunk-three"`):
					time.Sleep(40 * time.Millisecond)
					return `{"summary":"summary-3","tags":["Tag-3"],"category":"Cat-3"}`, nil
				case strings.Contains(prompt, `"content": "chunk-four"`):
					time.Sleep(20 * time.Millisecond)
					return `{"summary":"summary-4","tags":["Tag-4"],"category":"Cat-4"}`, nil
				default:
					return "", fmt.Errorf("unexpected chunk prompt: %s", prompt)
				}
			case "main-model":
				mainPrompt = prompt
				return `{"summary":"final summary","tags":["Tag-1","Tag-2","Ignored"],"category":"FinalCat"}`, nil
			default:
				return "", fmt.Errorf("unexpected model %q", modelName)
			}
		},
	}

	result, err := agent.Analyze(context.Background(), models.Article{
		Title:   "Title",
		Preview: "Preview",
		Content: "chunk-one\n\nchunk-two\n\nchunk-three\n\nchunk-four",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxActiveCalls != 2 {
		t.Fatalf("expected max parallel chunk calls to be 2, got %d", maxActiveCalls)
	}
	if strings.Index(mainPrompt, "summary-1") >= strings.Index(mainPrompt, "summary-2") {
		t.Fatalf("expected summary-1 before summary-2 in main prompt: %q", mainPrompt)
	}
	if strings.Index(mainPrompt, "summary-2") >= strings.Index(mainPrompt, "summary-3") {
		t.Fatalf("expected summary-2 before summary-3 in main prompt: %q", mainPrompt)
	}
	if strings.Index(mainPrompt, "summary-3") >= strings.Index(mainPrompt, "summary-4") {
		t.Fatalf("expected summary-3 before summary-4 in main prompt: %q", mainPrompt)
	}
	if result.Summary != "final summary" {
		t.Fatalf("unexpected final summary: %q", result.Summary)
	}
	wantTags := []string{"Tag-1", "Tag-2"}
	if len(result.Tags) != len(wantTags) {
		t.Fatalf("unexpected final tags: %#v", result.Tags)
	}
	for i := range wantTags {
		if result.Tags[i] != wantTags[i] {
			t.Fatalf("unexpected final tags: %#v", result.Tags)
		}
	}
}

func TestAnalyzeAllowsPartialChunkFailures(t *testing.T) {
	cfg := normalizeAIConfig(config.AIConfig{
		BaseURL:             "https://example.com",
		APIKey:              "test-key",
		ChunkModel:          "chunk-model",
		MainModel:           "main-model",
		ChunkSize:           100,
		MaxChunkParallelism: 2,
		TopNTags:            2,
	})

	mainCalls := 0
	agent := &EINOArticleAnalysisAgent{
		cfg: cfg,
		callModel: func(ctx context.Context, modelName string, messages []*schema.Message) (string, error) {
			prompt := fmt.Sprint(messages[len(messages)-1].Content)
			switch modelName {
			case "chunk-model":
				if strings.Contains(prompt, `"content": "bad-chunk"`) {
					return "", context.DeadlineExceeded
				}
				return `{"summary":"usable summary","tags":["Go"],"category":"Tech"}`, nil
			case "main-model":
				mainCalls++
				return `{"summary":"final summary","tags":["Go"],"category":"Tech"}`, nil
			default:
				return "", errors.New("unexpected model")
			}
		},
	}

	result, err := agent.Analyze(context.Background(), models.Article{
		Title:   "Title",
		Preview: "Preview",
		Content: "good-chunk\n\nbad-chunk",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mainCalls != 1 {
		t.Fatalf("expected main agent to run once, got %d", mainCalls)
	}
	if len(result.Tags) != 1 || result.Tags[0] != "Go" {
		t.Fatalf("unexpected final result: %#v", result)
	}
}

func TestAnalyzeFailsWhenAllChunksFail(t *testing.T) {
	cfg := normalizeAIConfig(config.AIConfig{
		BaseURL:             "https://example.com",
		APIKey:              "test-key",
		ChunkModel:          "chunk-model",
		MainModel:           "main-model",
		ChunkSize:           100,
		MaxChunkParallelism: 2,
	})

	mainCalled := false
	agent := &EINOArticleAnalysisAgent{
		cfg: cfg,
		callModel: func(ctx context.Context, modelName string, messages []*schema.Message) (string, error) {
			if modelName == "main-model" {
				mainCalled = true
			}
			return "", context.Canceled
		},
	}

	_, err := agent.Analyze(context.Background(), models.Article{
		Title:   "Title",
		Preview: "Preview",
		Content: "chunk-one\n\nchunk-two",
	})
	if err == nil {
		t.Fatal("expected analyze to fail when all chunks fail")
	}
	if mainCalled {
		t.Fatal("expected main agent to be skipped when all chunks fail")
	}
}

func TestAnalyzeFailsWhenMainAgentFails(t *testing.T) {
	cfg := normalizeAIConfig(config.AIConfig{
		BaseURL:             "https://example.com",
		APIKey:              "test-key",
		ChunkModel:          "chunk-model",
		MainModel:           "main-model",
		ChunkSize:           100,
		MaxChunkParallelism: 2,
	})

	agent := &EINOArticleAnalysisAgent{
		cfg: cfg,
		callModel: func(ctx context.Context, modelName string, messages []*schema.Message) (string, error) {
			if modelName == "chunk-model" {
				return `{"summary":"chunk summary","tags":["Go"],"category":"Tech"}`, nil
			}
			return "", context.DeadlineExceeded
		},
	}

	_, err := agent.Analyze(context.Background(), models.Article{
		Title:   "Title",
		Preview: "Preview",
		Content: "chunk-one\n\nchunk-two",
	})
	if err == nil {
		t.Fatal("expected analyze to fail when main agent fails")
	}
}

func assertChunkContents(t *testing.T, chunks []articleChunk, want []string) {
	t.Helper()
	if len(chunks) != len(want) {
		t.Fatalf("unexpected chunk count: got %d want %d (%#v)", len(chunks), len(want), chunks)
	}
	for i := range want {
		if chunks[i].Content != want[i] {
			t.Fatalf("unexpected chunk %d: got %q want %q", i, chunks[i].Content, want[i])
		}
		if chunks[i].Index != i {
			t.Fatalf("unexpected chunk index at %d: got %d", i, chunks[i].Index)
		}
	}
}
