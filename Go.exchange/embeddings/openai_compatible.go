package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"Go.exchange/config"
)

type OpenAICompatibleEmbedder struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
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
	return &OpenAICompatibleEmbedder{endpoint: endpoint, apiKey: apiKey, model: model, client: &http.Client{Timeout: timeout}}, nil
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
		return EmbedResult{}, fmt.Errorf("embedding provider returned status %d", response.StatusCode)
	}
	var payload embeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return EmbedResult{}, fmt.Errorf("decode embedding provider response: %w", err)
	}
	vectors, err := validateEmbeddingData(payload.Data, len(texts))
	if err != nil {
		return EmbedResult{}, err
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		model = e.model
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
