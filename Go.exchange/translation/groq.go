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

type GroqConfig struct {
	BaseURL             string
	APIKey              string
	Model               string
	PromptVersion       string
	Timeout             time.Duration
	MaxCompletionTokens int
}

type GroqProvider struct {
	baseURL             string
	apiKey              string
	model               string
	promptVersion       string
	maxCompletionTokens int
	client              *http.Client
}

func NewGroqProvider(config GroqConfig) *GroqProvider {
	return newGroqProvider(config, &http.Client{Timeout: normalizedTimeout(config.Timeout)})
}

// NewGroqProviderWithClient keeps the provider HTTP behavior testable without
// changing production to use the global default HTTP client.
func NewGroqProviderWithClient(config GroqConfig, client *http.Client) *GroqProvider {
	return newGroqProvider(config, client)
}

func newGroqProvider(config GroqConfig, client *http.Client) *GroqProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = DefaultModel
	}
	promptVersion := strings.TrimSpace(config.PromptVersion)
	if promptVersion == "" {
		promptVersion = DefaultPromptVersion
	}
	maxCompletionTokens := config.MaxCompletionTokens
	if maxCompletionTokens <= 0 {
		maxCompletionTokens = DefaultMaxCompletionTokens
	}
	if client == nil {
		client = &http.Client{Timeout: normalizedTimeout(config.Timeout)}
	}
	return &GroqProvider{
		baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), model: model,
		promptVersion: promptVersion, maxCompletionTokens: maxCompletionTokens, client: client,
	}
}

func normalizedTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

type groqCompletionRequest struct {
	Model               string        `json:"model"`
	Temperature         int           `json:"temperature"`
	ReasoningEffort     string        `json:"reasoning_effort"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
	Messages            []groqMessage `json:"messages"`
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqCompletionResponse struct {
	Choices []groqChoice `json:"choices"`
}

type groqChoice struct {
	Message *groqMessage `json:"message"`
}

func (p *GroqProvider) Translate(ctx context.Context, req Request) (ProviderResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil || strings.TrimSpace(p.apiKey) == "" || p.client == nil {
		return ProviderResult{}, newProviderError(ProviderErrorMisconfigured, 0, ErrProviderMisconfigured)
	}
	target, ok := NormalizeTargetLanguage(req.TargetLanguage)
	if !ok {
		return ProviderResult{}, ErrInvalidTargetLanguage
	}
	systemPrompt, userPrompt, err := BuildPrompt(req.SourceLanguage, target, req.Content)
	if err != nil {
		return ProviderResult{}, err
	}
	payload, err := json.Marshal(groqCompletionRequest{
		Model: p.model, Temperature: 0, ReasoningEffort: "none",
		MaxCompletionTokens: p.maxCompletionTokens,
		Messages: []groqMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
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
	var decoded groqCompletionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, err)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message == nil {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider choices are missing"))
	}
	translated := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if translated == "" {
		return ProviderResult{}, newProviderError(ProviderErrorInvalidResponse, response.StatusCode, errors.New("provider content is empty"))
	}
	return ProviderResult{Translation: translated}, nil
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
