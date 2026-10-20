package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestResolveEmbeddingEndpointForCloudflareWorkersAI(t *testing.T) {
	endpoint, err := resolveEmbeddingEndpoint(
		"https://api.cloudflare.com/client/v4/accounts/test-account/ai/v1",
	)
	if err != nil {
		t.Fatal(err)
	}

	want := "https://api.cloudflare.com/client/v4/accounts/test-account/ai/v1/embeddings"
	if endpoint != want {
		t.Fatalf("endpoint=%q want=%q", endpoint, want)
	}
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

func TestOpenAICompatibleEmbedderHTTPStatusFailuresStayProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "503", status: http.StatusServiceUnavailable},
		{name: "422", status: http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embedder, _ := testEmbedder(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "provider failure", test.status)
			})
			_, err := embedder.Embed(context.Background(), []string{"text"})
			var providerErr *ProviderHTTPError
			if !errors.As(err, &providerErr) || providerErr.StatusCode != test.status {
				t.Fatalf("error=%T %v want ProviderHTTPError status %d", err, err, test.status)
			}
			if IsProviderContractError(err) {
				t.Fatalf("HTTP status error was classified as a response contract error: %v", err)
			}
		})
	}
}

func TestProviderContractErrorClassification(t *testing.T) {
	cause := errors.New("invalid response")
	contractErr := newProviderContractError(cause)
	if !IsProviderContractError(contractErr) {
		t.Fatal("typed provider contract error was not recognized")
	}
	if !errors.Is(contractErr, cause) {
		t.Fatal("provider contract error does not unwrap to its cause")
	}
	if IsProviderContractError(errors.New("unknown provider failure")) {
		t.Fatal("untyped provider error was classified as a contract error")
	}
	if got := newProviderContractError(nil); got != nil {
		t.Fatalf("nil cause produced a provider contract error: %v", got)
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
		{name: "empty model", body: `{"model":"  ","data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3,4]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embedder, _ := testEmbedder(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(test.body))
			})
			_, err := embedder.Embed(context.Background(), []string{"a", "b"})
			if err == nil || !IsProviderContractError(err) {
				t.Fatalf("expected typed provider contract error, got %v", err)
			}
		})
	}
}

func TestOpenAICompatibleEmbedderClassifiesNonFiniteContractResponse(t *testing.T) {
	_, validationErr := validateEmbeddingData([]struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}{{Index: 0, Embedding: []float32{float32(math.NaN())}}}, 1)
	if validationErr == nil {
		t.Fatal("expected non-finite vector validation error")
	}
	contractErr := newProviderContractError(validationErr)
	if !IsProviderContractError(contractErr) || !errors.Is(contractErr, validationErr) {
		t.Fatalf("non-finite validation error classification: contract=%t err=%v", IsProviderContractError(contractErr), contractErr)
	}
}

func TestOpenAICompatibleEmbedderRejectsEmptyProviderModel(t *testing.T) {
	embedder, _ := testEmbedder(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"   ","data":[{"index":0,"embedding":[1,2]}]}`))
	})
	result, err := embedder.Embed(context.Background(), []string{"text"})
	if err == nil || !IsProviderContractError(err) {
		t.Fatalf("empty provider model error=%v contract=%t", err, IsProviderContractError(err))
	}
	if result.Model == "test-model" {
		t.Fatalf("empty provider model fell back to configured model: %#v", result)
	}
}

func TestProviderHTTPErrorExtractsBoundedMetadata(t *testing.T) {
	now := time.Date(2026, time.October, 20, 1, 2, 3, 0, time.UTC)
	response := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Retry-After":  []string{"12"},
			"X-Request-Id": []string{"req-123\nignored"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"code":"rate_limit_exceeded","type":"rate_limit_error","message":"secret message"}}`)),
	}
	providerErr := parseProviderHTTPError(response, now)
	if providerErr.StatusCode != http.StatusTooManyRequests || providerErr.ProviderCode != "rate_limit_exceeded" || providerErr.ProviderType != "rate_limit_error" || providerErr.RequestID != "req-123ignored" {
		t.Fatalf("provider error metadata=%+v", providerErr)
	}
	if !providerErr.RetryAfterProvided || !providerErr.RetryAfterValid || providerErr.RetryAfter != 12*time.Second {
		t.Fatalf("Retry-After metadata=%+v", providerErr)
	}
	if strings.Contains(providerErr.Error(), "secret message") {
		t.Fatalf("provider error leaked raw response text: %v", providerErr)
	}
}

func TestProviderHTTPErrorMetadataFieldsAreLengthLimited(t *testing.T) {
	longField := strings.Repeat("a", 256)
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Request-Id": []string{longField}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"` + longField + `","type":"` + longField + `"}}`)),
	}
	providerErr := parseProviderHTTPError(response, time.Now().UTC())
	if len(providerErr.ProviderCode) > 128 || len(providerErr.ProviderType) > 128 || len(providerErr.RequestID) > 128 {
		t.Fatalf("provider metadata exceeded bounds: code=%d type=%d request_id=%d", len(providerErr.ProviderCode), len(providerErr.ProviderType), len(providerErr.RequestID))
	}
}

func TestProviderRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, time.October, 20, 1, 2, 3, 0, time.UTC)
	wantDate := now.Add(45 * time.Second)
	tests := []struct {
		name  string
		value string
		want  time.Duration
		valid bool
	}{
		{name: "integer seconds", value: "9", want: 9 * time.Second, valid: true},
		{name: "http date", value: wantDate.Format(http.TimeFormat), want: 45 * time.Second, valid: true},
		{name: "past date", value: now.Add(-time.Minute).Format(http.TimeFormat), valid: true},
		{name: "missing", value: "", valid: false},
		{name: "negative", value: "-1", valid: false},
		{name: "overflow seconds", value: "18446744073709551615", valid: false},
		{name: "unrepresentable duration", value: "9223372037", valid: false},
		{name: "invalid text", value: "later", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := parseProviderRetryAfter(test.value, now)
			if valid != test.valid || got != test.want {
				t.Fatalf("parsed=%s valid=%t want=%s valid=%t", got, valid, test.want, test.valid)
			}
		})
	}
	longDelay, valid := parseProviderRetryAfter("2592000", now)
	if !valid || longDelay != 30*24*time.Hour {
		t.Fatalf("long Retry-After=%s valid=%t, want 30 days and preserved", longDelay, valid)
	}
}

func TestProviderHTTPErrorBodyReadIsBounded(t *testing.T) {
	body := `{"error":{"code":"bounded"},"padding":"` + strings.Repeat("x", providerErrorBodyLimit*2) + `"}`
	read := &countingProviderErrorBody{reader: strings.NewReader(body)}
	providerErr := parseProviderHTTPError(&http.Response{StatusCode: 429, Body: read}, time.Now())
	if read.bytesRead != providerErrorBodyLimit {
		t.Fatalf("error response read %d bytes, want at most %d", read.bytesRead, providerErrorBodyLimit)
	}
	if providerErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status was lost after bounded body read: %+v", providerErr)
	}
}

type countingProviderErrorBody struct {
	reader    *strings.Reader
	bytesRead int
}

func (b *countingProviderErrorBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.bytesRead += n
	return n, err
}

func (*countingProviderErrorBody) Close() error { return nil }

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
