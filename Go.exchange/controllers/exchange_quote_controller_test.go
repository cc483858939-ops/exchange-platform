package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/services"

	"github.com/gin-gonic/gin"
)

type stubExchangeQuoteReader struct {
	currencies services.CurrencyList
	quote      services.Quote
	err        error
}

func (s stubExchangeQuoteReader) Currencies(context.Context) (services.CurrencyList, error) {
	return s.currencies, s.err
}

func (s stubExchangeQuoteReader) Quote(context.Context, string, string, string) (services.Quote, error) {
	return s.quote, s.err
}

func TestGetExchangeQuoteReturnsQuote(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := liveExchangeQuoteReader
	liveExchangeQuoteReader = stubExchangeQuoteReader{quote: services.Quote{From: "CNY", To: "JPY", Amount: "100", Rate: "20", ConvertedAmount: "2000", Freshness: services.FreshnessFresh}}
	t.Cleanup(func() { liveExchangeQuoteReader = original })

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/exchange/quote?from=CNY&to=JPY&amount=100", nil)
	GetExchangeQuote(ctx)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body services.Quote
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ConvertedAmount != "2000" {
		t.Fatalf("convertedAmount = %q, want %q", body.ConvertedAmount, "2000")
	}
}

func TestGetExchangeQuoteErrorContract(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantError  string
	}{
		{
			name:       "invalid currency",
			err:        services.ErrInvalidCurrency,
			wantStatus: http.StatusBadRequest,
			wantCode:   exchangeErrorInvalidCurrency,
			wantError:  "Invalid currency.",
		},
		{
			name:       "invalid amount",
			err:        services.ErrInvalidAmount,
			wantStatus: http.StatusBadRequest,
			wantCode:   exchangeErrorInvalidAmount,
			wantError:  "Invalid amount.",
		},
		{
			name: "joined unsupported currency",
			// Internal context must not appear in the API response.
			err:        errors.Join(services.ErrUnsupportedCurrency, errors.New("missing USD in snapshot")),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   exchangeErrorUnsupportedCurrency,
			wantError:  "Currency is not supported.",
		},
		{
			name:       "generic service error",
			err:        errors.New("provider connection details"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   exchangeErrorUnavailable,
			wantError:  "Exchange-rate service is temporarily unavailable.",
		},
	}
	gin.SetMode(gin.TestMode)
	original := liveExchangeQuoteReader
	t.Cleanup(func() { liveExchangeQuoteReader = original })

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			liveExchangeQuoteReader = stubExchangeQuoteReader{err: test.err}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/exchange/quote?from=CNY&to=USD&amount=100", nil)

			GetExchangeQuote(ctx)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body exchangeErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, test.wantCode)
			}
			if body.Error != test.wantError {
				t.Fatalf("error = %q, want %q", body.Error, test.wantError)
			}
		})
	}
}

func TestGetExchangeCurrenciesMapsUnavailableTo503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := liveExchangeQuoteReader
	liveExchangeQuoteReader = stubExchangeQuoteReader{err: services.ErrNoRateSnapshot}
	t.Cleanup(func() { liveExchangeQuoteReader = original })

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/exchange/currencies", nil)
	GetExchangeCurrencies(ctx)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body exchangeErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != exchangeErrorUnavailable || body.Error != "Exchange-rate service is temporarily unavailable." {
		t.Fatalf("error body = %#v", body)
	}
}
