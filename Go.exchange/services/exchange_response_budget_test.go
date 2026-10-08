package services

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type rateBudgetBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *rateBudgetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *rateBudgetBody) Close() error { b.closed = true; return nil }

type rateBudgetTransport func(*http.Request) (*http.Response, error)

func (f rateBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExchangeResponseBudgetBoundsUnknownLengthAndClosesBody(t *testing.T) {
	normal := `[{"date":"2026-10-04","base":"USD","quote":"EUR","rate":0.9}]`
	for _, tc := range []struct {
		name, body string
		length     int64
		valid      bool
	}{
		{"boundary", normal + strings.Repeat(" ", 128-len(normal)), -1, true},
		{"oversized unknown", normal + strings.Repeat(" ", 300), -1, false},
		{"oversized declared", normal, 300, false},
		{"truncated", normal[:len(normal)-2], -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &rateBudgetBody{Reader: strings.NewReader(tc.body)}
			provider := FrankfurterProvider{Base: "USD", Endpoint: "https://test.invalid", MaxResponseBytes: 128, Client: &http.Client{Transport: rateBudgetTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: body, ContentLength: tc.length}, nil
			})}}
			_, err := provider.Fetch(t.Context())
			if (err == nil) != tc.valid || body.read > 129 || !body.closed {
				t.Fatalf("valid=%t err=%v bytes=%d closed=%t", tc.valid, err, body.read, body.closed)
			}
		})
	}
}
