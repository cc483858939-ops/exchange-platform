package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/sync/errgroup"
)

const (
	defaultAITimeoutSeconds    = 30
	defaultChunkSize           = 1200
	defaultTopNTags            = 5
	defaultMaxChunkParallelism = 3
)

var (
	primarySplitPunctuation = map[rune]struct{}{
		'。': {}, '！': {}, '？': {}, '!': {}, '?': {}, '；': {}, ';': {},
	}
	secondarySplitPunctuation = map[rune]struct{}{
		'，': {}, ',': {}, '、': {}, '：': {}, ':': {},
	}
)

// ArticleAnalyzer abstracts article analysis so the worker can be tested without real model calls.
type ArticleAnalyzer interface {
	Analyze(ctx context.Context, article models.Article) (ArticleAnalysisResult, error)
}

// ArticleAnalysisResult is the final article-level payload written back to the database.
type ArticleAnalysisResult struct {
	Summary  string   `json:"summary"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
}

// ChunkAnalysisResult is the extracted payload for a single chunk.
type ChunkAnalysisResult struct {
	ChunkIndex int      `json:"chunk_index"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Category   string   `json:"category"`
}

// TagCandidate stores one aggregated tag candidate and its frequency.
type TagCandidate struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// CategoryCount stores one aggregated category candidate and its frequency.
type CategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// AggregatedChunkContext is the deterministic summary produced before the main agent runs.
type AggregatedChunkContext struct {
	ChunkSummaries []string        `json:"chunk_summaries"`
	TagCandidates  []TagCandidate  `json:"tag_candidates"`
	CategoryCounts []CategoryCount `json:"category_counts"`
	SuccessChunks  int             `json:"success_chunks"`
	FailedChunks   int             `json:"failed_chunks"`
}

type articleChunk struct {
	Index   int
	Content string
}

type splitChunksPayload struct {
	Article models.Article
	Chunks  []articleChunk
}

type extractedChunksPayload struct {
	Article      models.Article
	Results      []ChunkAnalysisResult
	FailedChunks int
}

type mainAgentPayload struct {
	Article    models.Article
	Aggregated AggregatedChunkContext
}

type mainAgentRawPayload struct {
	Raw         string
	AllowedTags []TagCandidate
	TopNTags    int
}

type chunkExecutionOutcome struct {
	Result ChunkAnalysisResult
	OK     bool
	Err    error
}

type modelCaller func(ctx context.Context, modelName string, messages []*schema.Message) (string, error)

type EINOArticleAnalysisAgent struct {
	cfg       config.AIConfig
	callModel modelCaller
	pipeline  compose.Runnable[models.Article, ArticleAnalysisResult]
}

func NewEINOArticleAnalysisAgent(cfg config.AIConfig) (*EINOArticleAnalysisAgent, error) {
	normalized := normalizeAIConfig(cfg)
	if normalized.BaseURL == "" {
		return nil, errors.New("ai base_url is required")
	}
	if normalized.APIKey == "" {
		return nil, errors.New("ai api_key is required")
	}
	if normalized.ChunkModel == "" {
		return nil, errors.New("ai chunk_model or model is required")
	}
	if normalized.MainModel == "" {
		return nil, errors.New("ai main_model or model is required")
	}

	agent := &EINOArticleAnalysisAgent{cfg: normalized}
	agent.callModel = agent.defaultModelCaller()

	pipeline, err := agent.buildPipeline(context.Background())
	if err != nil {
		return nil, fmt.Errorf("build article analysis chain: %w", err)
	}
	agent.pipeline = pipeline
	return agent, nil
}

func normalizeAIConfig(cfg config.AIConfig) config.AIConfig {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.ChunkModel = strings.TrimSpace(cfg.ChunkModel)
	cfg.MainModel = strings.TrimSpace(cfg.MainModel)

	if cfg.ChunkModel == "" {
		cfg.ChunkModel = cfg.Model
	}
	if cfg.MainModel == "" {
		cfg.MainModel = cfg.Model
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultAITimeoutSeconds
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.ChunkOverlap < 0 || cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = 0
	}
	if cfg.MaxChunkParallelism <= 0 {
		cfg.MaxChunkParallelism = defaultMaxChunkParallelism
	}
	if cfg.TopNTags <= 0 {
		cfg.TopNTags = defaultTopNTags
	}

	return cfg
}

func (a *EINOArticleAnalysisAgent) defaultModelCaller() modelCaller {
	return func(ctx context.Context, modelName string, messages []*schema.Message) (string, error) {
		chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			BaseURL: a.cfg.BaseURL,
			APIKey:  a.cfg.APIKey,
			Model:   modelName,
			Timeout: time.Duration(a.cfg.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return "", fmt.Errorf("create chat model %s: %w", modelName, err)
		}

		response, err := chatModel.Generate(ctx, messages)
		if err != nil {
			return "", err
		}
		if response == nil {
			return "", errors.New("empty model response")
		}
		return response.Content, nil
	}
}

func (a *EINOArticleAnalysisAgent) Analyze(ctx context.Context, article models.Article) (ArticleAnalysisResult, error) {
	pipeline := a.pipeline
	if pipeline == nil {
		var err error
		pipeline, err = a.buildPipeline(context.Background())
		if err != nil {
			return ArticleAnalysisResult{}, fmt.Errorf("build article analysis chain: %w", err)
		}
	}

	return pipeline.Invoke(ctx, article)
}

func (a *EINOArticleAnalysisAgent) buildPipeline(ctx context.Context) (compose.Runnable[models.Article, ArticleAnalysisResult], error) {
	chain := compose.NewChain[models.Article, ArticleAnalysisResult]()
	chain.
		AppendLambda(compose.InvokableLambda(a.splitArticle), compose.WithNodeKey("splitter")).
		AppendLambda(compose.InvokableLambda(a.extractChunksInParallel), compose.WithNodeKey("parallel_extract")).
		AppendLambda(compose.InvokableLambda(a.aggregateForMainAgent), compose.WithNodeKey("aggregate")).
		AppendLambda(compose.InvokableLambda(a.invokeMainAgent), compose.WithNodeKey("main_agent")).
		AppendLambda(compose.InvokableLambda(a.normalizeMainAgentResult), compose.WithNodeKey("normalize"))

	return chain.Compile(ctx)
}

func (a *EINOArticleAnalysisAgent) splitArticle(ctx context.Context, article models.Article) (splitChunksPayload, error) {
	chunks := chunkArticle(article.Content, a.cfg.ChunkSize, a.cfg.ChunkOverlap)
	if len(chunks) == 0 {
		return splitChunksPayload{}, errors.New("article content produced no chunks")
	}

	return splitChunksPayload{Article: article, Chunks: chunks}, nil
}

func (a *EINOArticleAnalysisAgent) extractChunksInParallel(ctx context.Context, payload splitChunksPayload) (extractedChunksPayload, error) {
	if len(payload.Chunks) == 0 {
		return extractedChunksPayload{}, errors.New("article content produced no chunks")
	}

	outcomes := make([]chunkExecutionOutcome, len(payload.Chunks))
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, a.cfg.MaxChunkParallelism)
	// 创建一个容量为 MaxChunkParallelism 的 Channel，当作信号量（Semaphore）使用
	for i, chunk := range payload.Chunks {
		i, chunk := i, chunk
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-gctx.Done():
				return gctx.Err()
			}

			result, err := a.extractChunk(gctx, payload.Article, chunk, len(payload.Chunks))
			if err != nil {
				log.Printf("[Task] article %d chunk %d/%d analysis failed: %v", payload.Article.ID, chunk.Index+1, len(payload.Chunks), err)
				outcomes[i] = chunkExecutionOutcome{Err: err}
				return nil
			}

			outcomes[i] = chunkExecutionOutcome{Result: result, OK: true}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return extractedChunksPayload{}, err
	}

	results := make([]ChunkAnalysisResult, 0, len(payload.Chunks))
	failedChunks := 0
	for _, outcome := range outcomes {
		if outcome.OK {
			results = append(results, outcome.Result)
			continue
		}
		if outcome.Err != nil {
			failedChunks++
		}
	}

	return extractedChunksPayload{
		Article:      payload.Article,
		Results:      results,
		FailedChunks: failedChunks,
	}, nil
}

func (a *EINOArticleAnalysisAgent) aggregateForMainAgent(ctx context.Context, payload extractedChunksPayload) (mainAgentPayload, error) {
	if len(payload.Results) == 0 {
		return mainAgentPayload{}, errors.New("article analysis produced no successful chunks")
	}

	return mainAgentPayload{
		Article:    payload.Article,
		Aggregated: aggregateChunks(payload.Results, payload.FailedChunks),
	}, nil
}

func (a *EINOArticleAnalysisAgent) invokeMainAgent(ctx context.Context, payload mainAgentPayload) (mainAgentRawPayload, error) {
	raw, err := a.callModel(ctx, a.cfg.MainModel, []*schema.Message{
		schema.SystemMessage(mainAnalysisSystemPrompt(a.cfg.TopNTags)),
		schema.UserMessage(buildMainAgentUserPrompt(payload.Article, payload.Aggregated, a.cfg.TopNTags)),
	})
	if err != nil {
		return mainAgentRawPayload{}, fmt.Errorf("call main model: %w", err)
	}

	return mainAgentRawPayload{
		Raw:         raw,
		AllowedTags: payload.Aggregated.TagCandidates,
		TopNTags:    a.cfg.TopNTags,
	}, nil
}

func (a *EINOArticleAnalysisAgent) normalizeMainAgentResult(ctx context.Context, payload mainAgentRawPayload) (ArticleAnalysisResult, error) {
	result, err := parseFinalArticleAnalysisResponse(payload.Raw, payload.AllowedTags, payload.TopNTags)
	if err != nil {
		return ArticleAnalysisResult{}, fmt.Errorf("parse main analysis response: %w", err)
	}
	return result, nil
}

func (a *EINOArticleAnalysisAgent) extractChunk(ctx context.Context, article models.Article, chunk articleChunk, totalChunks int) (ChunkAnalysisResult, error) {
	raw, err := a.callModel(ctx, a.cfg.ChunkModel, []*schema.Message{
		schema.SystemMessage(chunkAnalysisSystemPrompt),
		schema.UserMessage(buildChunkAnalysisUserPrompt(article, chunk, totalChunks)),
	})
	if err != nil {
		return ChunkAnalysisResult{}, fmt.Errorf("call chunk model: %w", err)
	}

	result, err := parseArticleAnalysisResponse(raw)
	if err != nil {
		return ChunkAnalysisResult{}, fmt.Errorf("parse chunk response: %w", err)
	}

	return ChunkAnalysisResult{
		ChunkIndex: chunk.Index,
		Summary:    result.Summary,
		Tags:       result.Tags,
		Category:   result.Category,
	}, nil
}

const chunkAnalysisSystemPrompt = `You extract structured signals from a single article chunk.
Return only valid JSON with this shape:
{"summary":"...","tags":["..."],"category":"..."}
Rules:
- summary must be concise and stay in the same language as the chunk.
- tags must contain 1 to 5 short labels.
- category must be a single short label.
- Do not include markdown fences or extra text.`

func mainAnalysisSystemPrompt(topN int) string {
	return fmt.Sprintf(`You are the main agent for article synthesis.
Return only valid JSON with this shape:
{"summary":"...","tags":["..."],"category":"..."}
Rules:
- summary must be concise and stay in the same language as the article.
- tags must come from the provided candidate tags only.
- tags must contain 1 to %d short labels.
- category must be a single short label.
- Do not include markdown fences or extra text.`, topN)
}

func buildChunkAnalysisUserPrompt(article models.Article, chunk articleChunk, totalChunks int) string {
	payload := struct {
		Title       string `json:"title"`
		Preview     string `json:"preview"`
		ChunkIndex  int    `json:"chunk_index"`
		TotalChunks int    `json:"total_chunks"`
		Content     string `json:"content"`
	}{
		Title:       article.Title,
		Preview:     article.Preview,
		ChunkIndex:  chunk.Index + 1,
		TotalChunks: totalChunks,
		Content:     chunk.Content,
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("Title: %s\nPreview: %s\nChunk: %d/%d\nContent:\n%s", article.Title, article.Preview, chunk.Index+1, totalChunks, chunk.Content)
	}
	return string(body)
}

func buildMainAgentUserPrompt(article models.Article, aggregated AggregatedChunkContext, topN int) string {
	payload := struct {
		Title             string          `json:"title"`
		Preview           string          `json:"preview"`
		RequestedTopNTags int             `json:"requested_top_n_tags"`
		SuccessChunks     int             `json:"success_chunks"`
		FailedChunks      int             `json:"failed_chunks"`
		ChunkSummaries    []string        `json:"chunk_summaries"`
		TagCandidates     []TagCandidate  `json:"tag_candidates"`
		CategoryCounts    []CategoryCount `json:"category_counts"`
	}{
		Title:             article.Title,
		Preview:           article.Preview,
		RequestedTopNTags: topN,
		SuccessChunks:     aggregated.SuccessChunks,
		FailedChunks:      aggregated.FailedChunks,
		ChunkSummaries:    aggregated.ChunkSummaries,
		TagCandidates:     aggregated.TagCandidates,
		CategoryCounts:    aggregated.CategoryCounts,
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("Title: %s\nPreview: %s\nRequestedTopNTags: %d", article.Title, article.Preview, topN)
	}
	return string(body)
}

func chunkArticle(content string, chunkSize, chunkOverlap int) []articleChunk {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = 0
	}

	normalized := normalizeContent(content)
	if normalized == "" {
		return nil
	}

	paragraphs := splitParagraphs(normalized)
	chunks := make([]articleChunk, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		for _, chunkContent := range splitParagraphToChunks(paragraph, chunkSize, chunkOverlap) {
			if strings.TrimSpace(chunkContent) == "" {
				continue
			}
			chunks = append(chunks, articleChunk{
				Index:   len(chunks),
				Content: chunkContent,
			})
		}
	}

	return chunks
}

func normalizeContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func splitParagraphs(content string) []string {
	paragraphs := splitOnBlankLines(content)
	if len(paragraphs) <= 1 && strings.Contains(content, "\n") {
		paragraphs = splitOnSingleNewlines(content)
	}

	filtered := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		filtered = append(filtered, paragraph)
	}
	return filtered
}

func splitOnBlankLines(content string) []string {
	lines := strings.Split(content, "\n")
	paragraphs := make([]string, 0, len(lines))
	current := make([]string, 0, len(lines))

	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraphs = append(paragraphs, strings.Join(current, "\n"))
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	if len(paragraphs) == 0 && strings.TrimSpace(content) != "" {
		return []string{content}
	}
	return paragraphs
}

func splitOnSingleNewlines(content string) []string {
	lines := strings.Split(content, "\n")
	paragraphs := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paragraphs = append(paragraphs, line)
	}
	return paragraphs
}

func splitParagraphToChunks(paragraph string, chunkSize, chunkOverlap int) []string {
	if runeLen(paragraph) <= chunkSize {
		return []string{paragraph}
	} // 长度安全直接返回

	pieces := []string{paragraph}
	pieces = splitOversizedPiecesByPunctuation(pieces, chunkSize, primarySplitPunctuation)   // 一级标点切割
	pieces = splitOversizedPiecesByPunctuation(pieces, chunkSize, secondarySplitPunctuation) // 二级标点切割
	return packPiecesIntoChunks(pieces, chunkSize, chunkOverlap)                             //硬切
}

func splitOversizedPiecesByPunctuation(pieces []string, chunkSize int, punctuation map[rune]struct{}) []string {
	result := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		if runeLen(piece) <= chunkSize {
			result = append(result, piece)
			continue
		}

		split := splitByPunctuation(piece, punctuation)
		if len(split) <= 1 {
			result = append(result, piece)
			continue
		}
		result = append(result, split...)
	}
	return result
}

func splitByPunctuation(content string, punctuation map[rune]struct{}) []string {
	runes := []rune(content)
	pieces := make([]string, 0, len(runes)/8+1)
	current := make([]rune, 0, len(runes))

	for _, r := range runes {
		current = append(current, r)
		if _, ok := punctuation[r]; ok {
			piece := string(current)
			if strings.TrimSpace(piece) != "" {
				pieces = append(pieces, piece)
			}
			current = current[:0]
		}
	}

	if len(current) > 0 {
		piece := string(current)
		if strings.TrimSpace(piece) != "" {
			pieces = append(pieces, piece)
		}
	}

	return pieces
}

func packPiecesIntoChunks(pieces []string, chunkSize, chunkOverlap int) []string {
	chunks := make([]string, 0, len(pieces))
	current := ""

	flushCurrent := func() {
		trimmed := strings.TrimSpace(current)
		if trimmed != "" {
			chunks = append(chunks, trimmed)
		}
		current = ""
	}

	for _, piece := range pieces {
		if strings.TrimSpace(piece) == "" {
			continue
		}

		if runeLen(piece) > chunkSize {
			flushCurrent()
			chunks = append(chunks, hardSplitByLength(piece, chunkSize, chunkOverlap)...)
			continue
		}

		if current == "" {
			current = piece
			continue
		}

		if runeLen(current)+runeLen(piece) <= chunkSize {
			current += piece
			continue
		}

		flushCurrent()
		current = piece
	}

	flushCurrent()
	return chunks
}

// hardSplitByLength 按固定长度对文本进行硬切分。
//
// 参数说明：
// - content: 原始文本内容
// - chunkSize: 每个分片的最大长度（按 rune 计数，避免中文被截坏）
// - chunkOverlap: 相邻分片之间的重叠长度
//
// 处理规则：
// - 如果 chunkSize <= 0，则使用默认分片大小 defaultChunkSize
// - 如果 chunkOverlap < 0 或 chunkOverlap >= chunkSize，则视为无重叠
// - 切分后会对每个分片做 strings.TrimSpace，空分片会被忽略
func hardSplitByLength(content string, chunkSize, chunkOverlap int) []string {
	// 兜底分片大小，避免传入非法 chunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	// overlap 不能为负，也不能大于等于 chunkSize，否则步长会异常
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = 0
	}

	// 转成 rune 切分，避免按字节切分时把中文等多字节字符截断
	runes := []rune(content)
	if len(runes) == 0 {
		return nil
	}

	// 实际每次向前推进的步长
	// 例如 chunkSize=100, chunkOverlap=20，则每次前进 80 个字符
	step := chunkSize - chunkOverlap

	// 预估容量，减少 append 过程中的扩容次数
	chunks := make([]string, 0, (len(runes)+step-1)/step)

	// 从头开始按 step 递进切分
	for start := 0; start < len(runes); start += step {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		// 截取当前分片，并去掉首尾空白字符
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}

		// 已经到最后一段，直接退出
		if end == len(runes) {
			break
		}
	}

	return chunks
}

func runeLen(content string) int {
	return len([]rune(content))
}

func aggregateChunks(results []ChunkAnalysisResult, failedChunks int) AggregatedChunkContext {
	sortedResults := append([]ChunkAnalysisResult(nil), results...)
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].ChunkIndex < sortedResults[j].ChunkIndex
	})

	aggregated := AggregatedChunkContext{
		ChunkSummaries: make([]string, 0, len(sortedResults)),
		SuccessChunks:  len(sortedResults),
		FailedChunks:   failedChunks,
	}

	type tagAggregate struct {
		Tag       string
		Count     int
		FirstSeen int
	}
	type categoryAggregate struct {
		Category  string
		Count     int
		FirstSeen int
	}

	tagMap := make(map[string]*tagAggregate)
	categoryMap := make(map[string]*categoryAggregate)
	nextTagOrder := 0
	nextCategoryOrder := 0

	for _, result := range sortedResults {
		aggregated.ChunkSummaries = append(aggregated.ChunkSummaries, result.Summary)

		for _, tag := range result.Tags {
			normalized := normalizeLabel(tag)
			if normalized == "" {
				continue
			}
			if existing, ok := tagMap[normalized]; ok {
				existing.Count++
				continue
			}
			tagMap[normalized] = &tagAggregate{
				Tag:       strings.TrimSpace(tag),
				Count:     1,
				FirstSeen: nextTagOrder,
			}
			nextTagOrder++
		}

		normalizedCategory := normalizeLabel(result.Category)
		if normalizedCategory == "" {
			continue
		}
		if existing, ok := categoryMap[normalizedCategory]; ok {
			existing.Count++
			continue
		}
		categoryMap[normalizedCategory] = &categoryAggregate{
			Category:  strings.TrimSpace(result.Category),
			Count:     1,
			FirstSeen: nextCategoryOrder,
		}
		nextCategoryOrder++
	}

	tagAggregates := make([]tagAggregate, 0, len(tagMap))
	for _, item := range tagMap {
		tagAggregates = append(tagAggregates, *item)
	}
	sort.Slice(tagAggregates, func(i, j int) bool {
		if tagAggregates[i].Count != tagAggregates[j].Count {
			return tagAggregates[i].Count > tagAggregates[j].Count
		}
		return tagAggregates[i].FirstSeen < tagAggregates[j].FirstSeen
	})
	aggregated.TagCandidates = make([]TagCandidate, 0, len(tagAggregates))
	for _, item := range tagAggregates {
		aggregated.TagCandidates = append(aggregated.TagCandidates, TagCandidate{Tag: item.Tag, Count: item.Count})
	}

	categoryAggregates := make([]categoryAggregate, 0, len(categoryMap))
	for _, item := range categoryMap {
		categoryAggregates = append(categoryAggregates, *item)
	}
	sort.Slice(categoryAggregates, func(i, j int) bool {
		if categoryAggregates[i].Count != categoryAggregates[j].Count {
			return categoryAggregates[i].Count > categoryAggregates[j].Count
		}
		return categoryAggregates[i].FirstSeen < categoryAggregates[j].FirstSeen
	})
	aggregated.CategoryCounts = make([]CategoryCount, 0, len(categoryAggregates))
	for _, item := range categoryAggregates {
		aggregated.CategoryCounts = append(aggregated.CategoryCounts, CategoryCount{Category: item.Category, Count: item.Count})
	}

	return aggregated
}

func parseArticleAnalysisResponse(raw string) (ArticleAnalysisResult, error) {
	clean := strings.TrimSpace(raw)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```JSON")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	var result ArticleAnalysisResult
	if err := json.Unmarshal([]byte(clean), &result); err != nil {
		return ArticleAnalysisResult{}, fmt.Errorf("decode article analysis response: %w", err)
	}

	result.Summary = strings.TrimSpace(result.Summary)
	result.Category = strings.TrimSpace(result.Category)

	filteredTags := make([]string, 0, len(result.Tags))
	seenTags := make(map[string]struct{}, len(result.Tags))
	for _, tag := range result.Tags {
		normalized := normalizeLabel(tag)
		if normalized == "" {
			continue
		}
		if _, exists := seenTags[normalized]; exists {
			continue
		}
		seenTags[normalized] = struct{}{}
		filteredTags = append(filteredTags, strings.TrimSpace(tag))
	}
	result.Tags = filteredTags

	if result.Summary == "" {
		return ArticleAnalysisResult{}, errors.New("article analysis summary is empty")
	}
	if result.Category == "" {
		return ArticleAnalysisResult{}, errors.New("article analysis category is empty")
	}
	if len(result.Tags) == 0 {
		return ArticleAnalysisResult{}, errors.New("article analysis tags are empty")
	}

	return result, nil
}

func parseFinalArticleAnalysisResponse(raw string, allowedTags []TagCandidate, topN int) (ArticleAnalysisResult, error) {
	result, err := parseArticleAnalysisResponse(raw)
	if err != nil {
		return ArticleAnalysisResult{}, err
	}

	if topN <= 0 {
		topN = defaultTopNTags
	}

	allowed := make(map[string]string, len(allowedTags))
	for _, candidate := range allowedTags {
		normalized := normalizeLabel(candidate.Tag)
		if normalized == "" {
			continue
		}
		allowed[normalized] = candidate.Tag
	}

	filtered := make([]string, 0, len(result.Tags))
	seen := make(map[string]struct{}, len(result.Tags))
	for _, tag := range result.Tags {
		normalized := normalizeLabel(tag)
		canonical, ok := allowed[normalized]
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		filtered = append(filtered, canonical)
		if len(filtered) == topN {
			break
		}
	}

	if len(filtered) == 0 {
		return ArticleAnalysisResult{}, errors.New("article analysis tags are not in the allowed candidate set")
	}

	result.Tags = filtered
	return result, nil
}

func normalizeLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}
