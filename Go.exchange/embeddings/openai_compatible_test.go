package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
)

func testEmbedder(t *testing.T, handler http.HandlerFunc) (*OpenAICompatibleEmbedder, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	embedder, err := NewOpenAICompatibleEmbedder(config.EmbeddingConfig{BaseURL: server.URL, APIKey: "secret-key", Model: "test-model", TimeoutSeconds: 2})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return embedder, server
}

func TestOpenAICompatibleEmbedderPostsOrderedVectorsAndAuth(t *testing.T) {
	embedder, server := testEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Fatalf("request method=%s path=%s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var request embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || len(request.Input) != 2 {
			t.Fatalf("request=%#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"served-model","data":[{"index":1,"embedding":[3,4]},{"index":0,"embedding":[1,2]}]}`))
	})
	result, err := embedder.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "served-model" || len(result.Vectors) != 2 || result.Vectors[0][0] != 1 || result.Vectors[1][0] != 3 {
		t.Fatalf("result=%#v", result)
	}
	_ = server
}

func TestOpenAICompatibleEmbedderRequiresAPIKey(t *testing.T) {
	_, err := NewOpenAICompatibleEmbedder(config.EmbeddingConfig{BaseURL: "https://embedding.example", Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "api key") {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenAICompatibleEmbedderRejectsProviderFailuresWithoutLeakingKey(t *testing.T) {
	embedder, _ := testEmbedder(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "provider failure", http.StatusBadGateway) })
	_, err := embedder.Embed(context.Background(), []string{"text"})
	if err == nil || strings.Contains(err.Error(), "secret-key") || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("err=%v", err)
	}
	var providerErr *ProviderHTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("typed provider error=%T %#v", err, err)
	}
}

func TestOpenAICompatibleEmbedderRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: "{"},
		{name: "count mismatch", body: `{"data":[{"index":0,"embedding":[1,2]}]}`},
		{name: "duplicate index", body: `{"data":[{"index":0,"embedding":[1,2]},{"index":0,"embedding":[3,4]}]}`},
		{name: "out of range index", body: `{"data":[{"index":2,"embedding":[1,2]}]}`},
		{name: "empty vector", body: `{"data":[{"index":0,"embedding":[]}]}`},
		{name: "inconsistent dimensions", body: `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embedder, _ := testEmbedder(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(test.body))
			})
			if _, err := embedder.Embed(context.Background(), []string{"a", "b"}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOpenAICompatibleEmbedderRejectsNonFiniteVector(t *testing.T) {
	_, err := validateEmbeddingData([]struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}{{Index: 0, Embedding: []float32{float32(math.NaN())}}}, 1)
	if err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenAICompatibleEmbedderHonorsContextCancellation(t *testing.T) {
	embedder, _ := testEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := embedder.Embed(ctx, []string{"text"})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(started))
	}
}
