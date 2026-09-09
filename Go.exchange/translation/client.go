package translation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxProviderResponseBodyBytes int64 = 1 << 20

const (
	DefaultMaxCompletionTokens = 2048
	DefaultTimeout             = 10 * time.Second
)

type ClientConfig struct {
	BaseURL             string
	APIKey              string
	Model               string
	Timeout             time.Duration
	MaxCompletionTokens int
}

type WorkersAIClient struct {
	baseURL             string
	apiKey              string
	model               string
	maxCompletionTokens int
	client              *http.Client
}

func NewWorkersAIClient(config ClientConfig) *WorkersAIClient {
	return newWorkersAIClient(config, &http.Client{Timeout: normalizedTimeout(config.Timeout)})
}

// NewWorkersAIClientWithHTTPClient keeps the client HTTP behavior testable without
// changing production to use the global default HTTP client.
func NewWorkersAIClientWithHTTPClient(config ClientConfig, client *http.Client) *WorkersAIClient {
	return newWorkersAIClient(config, client)
}

func newWorkersAIClient(config ClientConfig, client *http.Client) *WorkersAIClient {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	model := strings.TrimSpace(config.Model)
	maxCompletionTokens := config.MaxCompletionTokens
	if maxCompletionTokens <= 0 {
		maxCompletionTokens = DefaultMaxCompletionTokens
	}
	if client == nil {
		client = &http.Client{Timeout: normalizedTimeout(config.Timeout)}
	}
	return &WorkersAIClient{
		baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), model: model,
		maxCompletionTokens: maxCompletionTokens, client: client,
	}
}

func normalizedTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

type completionRequest struct {
	Model               string              `json:"model"`
	Messages            []completionMessage `json:"messages"`
	MaxCompletionTokens int                 `json:"max_completion_tokens"`
	ChatTemplateKwargs  chatTemplateKwargs  `json:"chat_template_kwargs"`
}

type chatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

type completionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionResponse struct {
	Choices []completionChoice `json:"choices"`
}

type completionChoice struct {
	Message *completionResponseMessage `json:"message"`
}

type completionResponseMessage struct {
	Content          string `json:"content"`
	Reasoning        string `json:"reasoning"`
	ReasoningContent string `json:"reasoning_content"`
}

func (p *WorkersAIClient) Translate(ctx context.Context, req Request) (ProviderResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil {
		return ProviderResult{}, newProviderError(ProviderErrorMisconfigured, 0, ErrProviderMisconfigured)
	}
	target, ok := NormalizeTargetLanguage(req.TargetLanguage)
	if !ok {
		return ProviderResult{}, ErrInvalidTargetLanguage
	}
	if strings.TrimSpace(p.baseURL) == "" || strings.TrimSpace(p.apiKey) == "" || strings.TrimSpace(p.model) == "" || p.client == nil {
		return ProviderResult{}, newProviderError(ProviderErrorMisconfigured, 0, ErrProviderMisconfigured)
	}
	systemPrompt, userPrompt, err := BuildPrompt(req.SourceLanguage, target, req.Content)
	if err != nil {
		return ProviderResult{}, err
	}
	payload, err := json.Marshal(completionRequest{
		Model:               p.model,
		MaxCompletionTokens: p.maxCompletionTokens,
		Messages: []completionMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ChatTemplateKwargs: chatTemplateKwargs{
			EnableThinking: false,
		},
	})
	if err != nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, 0, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return ProviderResult{}, newProviderError(ProviderErrorUnavailable, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := p.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isNetworkTimeout(err) {
			return ProviderResult{}, newProviderError(ProviderErrorTimeout, 0, err)
		}
		if errors.Is(err, context.Canceled) {
			return ProviderResult{}, err
		}
		return ProviderResult{}, newProviderError(ProviderErrorUnavailable, 0, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusTooManyRequests {
			retryAfter, hasRetryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
			rateLimitError := newProviderError(ProviderErrorRateLimited, response.StatusCode, nil)
			if hasRetryAfter {
				rateLimitError.RetryAfter = retryAfter.duration
				rateLimitError.RetryAfterHeader = retryAfter.raw
			}
			return ProviderResult{}, rateLimitError
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return ProviderResult{}, newProviderError(ProviderErrorMisconfigured, response.StatusCode, nil)
		}
		if response.StatusCode >= http.StatusInternalServerError {
			return ProviderResult{}, newProviderError(ProviderErrorUnavailable, response.StatusCode, nil)
		}
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, nil)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBodyBytes+1))
	if err != nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, err)
	}
	if int64(len(body)) > maxProviderResponseBodyBytes {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider response body too large"))
	}
	var decoded completionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, err)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message == nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider choices are missing"))
	}
	message := decoded.Choices[0].Message
	if strings.TrimSpace(message.Reasoning) != "" || strings.TrimSpace(message.ReasoningContent) != "" {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider returned reasoning content"))
	}
	translated := strings.TrimSpace(message.Content)
	if translated == "" {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider content is empty"))
	}
	if containsReasoningMarkup(translated) {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider returned reasoning markup"))
	}
	return ProviderResult{Translation: translated}, nil
}

func containsReasoningMarkup(content string) bool {
	normalized := strings.ToLower(content)
	return strings.Contains(normalized, "<think>") || strings.Contains(normalized, "</think>")
}

func isNetworkTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

type retryAfterValue struct {
	raw      string
	duration time.Duration
}

func parseRetryAfter(raw string) (retryAfterValue, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return retryAfterValue{}, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return retryAfterValue{raw: raw, duration: time.Duration(seconds) * time.Second}, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return retryAfterValue{}, false
	}
	duration := time.Until(when)
	if duration < 0 {
		duration = 0
	}
	return retryAfterValue{raw: raw, duration: duration}, true
}
