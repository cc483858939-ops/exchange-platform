package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
)

type OpenAICompatibleEmbedder struct {
	maxResponseBytes int64
	endpoint         string
	apiKey           string
	model            string
	client           *http.Client
}

type ProviderHTTPError struct {
	StatusCode         int
	ProviderCode       string
	ProviderType       string
	RetryAfter         time.Duration
	RetryAfterProvided bool
	RetryAfterValid    bool
	RequestID          string
}

func (e *ProviderHTTPError) Error() string {
	return fmt.Sprintf("embedding provider returned status %d", e.StatusCode)
}

type ProviderContractError struct {
	cause error
}

func (e *ProviderContractError) Error() string {
	return "embedding provider returned an invalid response"
}

func (e *ProviderContractError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newProviderContractError(err error) error {
	if err == nil {
		return nil
	}
	return &ProviderContractError{cause: err}
}

func IsProviderContractError(err error) bool {
	var contractErr *ProviderContractError
	return errors.As(err, &contractErr)
}

const providerErrorBodyLimit = 16 << 10

func parseProviderHTTPError(response *http.Response, now time.Time) *ProviderHTTPError {
	providerErr := &ProviderHTTPError{
		StatusCode: response.StatusCode,
		RequestID:  safeProviderMetadataField(response.Header.Get("x-request-id"), 128),
	}
	if response.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(response.Body, providerErrorBodyLimit))
		var envelope struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			providerErr.ProviderCode = safeProviderMetadataField(envelope.Error.Code, 128)
			providerErr.ProviderType = safeProviderMetadataField(envelope.Error.Type, 128)
		}
	}
	if retryAfter := strings.TrimSpace(response.Header.Get("Retry-After")); retryAfter != "" {
		providerErr.RetryAfterProvided = true
		providerErr.RetryAfter, providerErr.RetryAfterValid = parseProviderRetryAfter(retryAfter, now)
	}
	return providerErr
}

func parseProviderRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if isASCIIDigits(value) {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !retryAt.After(now) {
		return 0, true
	}
	maxWait := time.Duration(math.MaxInt64)
	if retryAt.After(now.Add(maxWait)) {
		return 0, false
	}
	return retryAt.Sub(now), true
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func safeProviderMetadataField(value string, maxBytes int) string {
	var safe strings.Builder
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			continue
		}
		if safe.Len()+1 > maxBytes {
			break
		}
		safe.WriteByte(byte(char))
	}
	return strings.TrimSpace(safe.String())
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func NewOpenAICompatibleEmbedder(cfg config.EmbeddingConfig) (*OpenAICompatibleEmbedder, error) {
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = 8 << 20
	}
	endpoint, err := resolveEmbeddingEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("embedding model is required")
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("embedding api key is required")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if maxResponseBytes == math.MaxInt64 {
		return nil, errors.New("embedding response budget is too large")
	}
	return &OpenAICompatibleEmbedder{endpoint: endpoint, apiKey: apiKey, model: model, client: &http.Client{Timeout: timeout}, maxResponseBytes: maxResponseBytes}, nil
}

func resolveEmbeddingEndpoint(baseURL string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if raw == "" {
		return "", errors.New("embedding base_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("embedding base_url is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("embedding base_url must use http or https")
	}
	switch {
	case strings.HasSuffix(parsed.Path, "/v1/embeddings"):
		return parsed.String(), nil
	case strings.HasSuffix(parsed.Path, "/v1"):
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/embeddings"
	default:
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/embeddings"
	}
	return parsed.String(), nil
}

func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, texts []string) (EmbedResult, error) {
	if len(texts) == 0 {
		return EmbedResult{}, errors.New("embedding input is empty")
	}
	body, err := json.Marshal(embeddingRequest{Model: e.model, Input: texts})
	if err != nil {
		return EmbedResult{}, fmt.Errorf("marshal embedding request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("create embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embedding provider request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return EmbedResult{}, parseProviderHTTPError(response, time.Now().UTC())
	}
	if response.ContentLength > e.maxResponseBytes {
		return EmbedResult{}, newProviderContractError(errors.New("embedding response exceeds byte budget"))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, e.maxResponseBytes+1))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("read embedding provider response: %w", err)
	}
	if int64(len(raw)) > e.maxResponseBytes {
		return EmbedResult{}, newProviderContractError(errors.New("embedding response exceeds byte budget"))
	}
	var payload embeddingResponse
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&payload); err != nil {
		return EmbedResult{}, newProviderContractError(fmt.Errorf("decode embedding provider response: %w", err))
	}
	vectors, err := validateEmbeddingData(payload.Data, len(texts))
	if err != nil {
		return EmbedResult{}, newProviderContractError(err)
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return EmbedResult{}, newProviderContractError(errors.New("embedding provider returned an empty model"))
	}
	return EmbedResult{Vectors: vectors, Model: model}, nil
}

func validateEmbeddingData(data []struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}, expected int) ([][]float32, error) {
	if len(data) != expected {
		return nil, fmt.Errorf("embedding response count=%d want=%d", len(data), expected)
	}
	vectors := make([][]float32, expected)
	dimensions := 0
	for _, item := range data {
		if item.Index < 0 || item.Index >= expected || vectors[item.Index] != nil {
			return nil, errors.New("embedding response indexes are invalid")
		}
		if len(item.Embedding) == 0 {
			return nil, errors.New("embedding vector is empty")
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		}
		if len(item.Embedding) != dimensions {
			return nil, errors.New("embedding dimensions are inconsistent")
		}
		for _, value := range item.Embedding {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, errors.New("embedding vector contains non-finite value")
			}
		}
		vectors[item.Index] = item.Embedding
	}
	for _, vector := range vectors {
		if vector == nil {
			return nil, errors.New("embedding response indexes are incomplete")
		}
	}
	return vectors, nil
}
