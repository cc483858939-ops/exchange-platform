package embeddings

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"Go.exchange/config"
)

type embeddingBudgetBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *embeddingBudgetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *embeddingBudgetBody) Close() error { b.closed = true; return nil }

type embeddingBudgetTransport func(*http.Request) (*http.Response, error)

func (f embeddingBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbeddingResponseBudgetBoundsUnknownLengthAndClassifiesPermanent(t *testing.T) {
	normal := `{"model":"test-model","data":[{"index":0,"embedding":[0.1,0.2]}]}`
	for _, tc := range []struct {
		name, body string
		length     int64
		valid      bool
	}{
		{"exact boundary", normal + strings.Repeat(" ", 128-len(normal)), -1, true},
		{"oversized unknown", normal + strings.Repeat(" ", 300), -1, false},
		{"oversized declared", normal, 300, false},
		{"truncated JSON", normal[:len(normal)-2], -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			embedder, err := NewOpenAICompatibleEmbedder(config.EmbeddingConfig{BaseURL: "https://test.invalid", APIKey: "test", Model: "test-model", MaxResponseBytes: 128})
			if err != nil {
				t.Fatal(err)
			}
			body := &embeddingBudgetBody{Reader: strings.NewReader(tc.body)}
			embedder.client = &http.Client{Transport: embeddingBudgetTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: body, ContentLength: tc.length}, nil
			})}
			_, err = embedder.Embed(t.Context(), []string{"input"})
			if (err == nil) != tc.valid || body.read > 129 || !body.closed {
				t.Fatalf("valid=%t err=%v bytes=%d closed=%t", tc.valid, err, body.read, body.closed)
			}
			if !tc.valid && (!IsProviderContractError(err) || IsRetryableProviderError(err)) {
				t.Fatalf("deterministic response failure was retryable: %v", err)
			}
		})
	}
}
