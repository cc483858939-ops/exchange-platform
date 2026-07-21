package controllers

import (
	"context"
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
	if got := response.Body.String(); got == "" || !contains(got, `"convertedAmount":"2000"`) {
		t.Fatalf("response = %s", got)
	}
}

func TestGetExchangeQuoteMapsUnsupportedCurrencyTo422(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := liveExchangeQuoteReader
	liveExchangeQuoteReader = stubExchangeQuoteReader{err: errors.Join(services.ErrUnsupportedCurrency, errors.New("missing in snapshot"))}
	t.Cleanup(func() { liveExchangeQuoteReader = original })

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/exchange/quote?from=CNY&to=ABC&amount=100", nil)
	GetExchangeQuote(ctx)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
}

func contains(value, needle string) bool {
	return len(value) >= len(needle) && stringContains(value, needle)
}

func stringContains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
