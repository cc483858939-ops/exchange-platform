package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubRateProvider struct {
	snapshot RateSnapshot
	err      error
}

func (p stubRateProvider) Fetch(context.Context) (RateSnapshot, error) {
	return p.snapshot, p.err
}

type memorySnapshotStore struct {
	snapshot RateSnapshot
	err      error
	hasValue bool
}

func (s *memorySnapshotStore) Load(context.Context) (RateSnapshot, error) {
	if s.err != nil {
		return RateSnapshot{}, s.err
	}
	if !s.hasValue {
		return RateSnapshot{}, ErrNoRateSnapshot
	}
	return s.snapshot, nil
}

func (s *memorySnapshotStore) Save(_ context.Context, snapshot RateSnapshot, _ time.Duration) error {
	if s.err != nil {
		return s.err
	}
	s.snapshot = snapshot
	s.hasValue = true
	return nil
}

func sampleSnapshot(fetchedAt time.Time) RateSnapshot {
	return RateSnapshot{
		Base:      "EUR",
		Rates:     map[string]string{"EUR": "1", "CNY": "7.5", "JPY": "160", "USD": "1.25"},
		Provider:  "test-provider",
		AsOf:      "2026-07-20",
		FetchedAt: fetchedAt,
	}
}

func TestRateServiceQuotesCrossCurrencyWithDecimalMath(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	service := NewRateService(
		stubRateProvider{snapshot: sampleSnapshot(now)},
		&memorySnapshotStore{},
		RateServiceOptions{FreshFor: time.Hour, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	quote, err := service.Quote(context.Background(), "cny", "jpy", "100")
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if quote.Rate != "21.3333333333" {
		t.Fatalf("Quote().Rate = %q, want %q", quote.Rate, "21.3333333333")
	}
	if quote.ConvertedAmount != "2133.333333" {
		t.Fatalf("Quote().ConvertedAmount = %q, want %q", quote.ConvertedAmount, "2133.333333")
	}
	if quote.Freshness != FreshnessFresh {
		t.Fatalf("Quote().Freshness = %q, want %q", quote.Freshness, FreshnessFresh)
	}
}

func TestRateServiceUsesStaleSnapshotWhenProviderFails(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	store := &memorySnapshotStore{snapshot: sampleSnapshot(now.Add(-time.Hour)), hasValue: true}
	service := NewRateService(
		stubRateProvider{err: errors.New("upstream unavailable")},
		store,
		RateServiceOptions{FreshFor: 30 * time.Minute, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	quote, err := service.Quote(context.Background(), "EUR", "USD", "2")
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if quote.ConvertedAmount != "2.5" || quote.Freshness != FreshnessStale {
		t.Fatalf("Quote() = %+v, want stale 2.5", quote)
	}
}

func TestRateServiceRejectsInvalidInputAndUnsupportedCurrencies(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	service := NewRateService(
		stubRateProvider{snapshot: sampleSnapshot(now)},
		&memorySnapshotStore{},
		RateServiceOptions{Now: func() time.Time { return now }},
	)

	if _, err := service.Quote(context.Background(), "US", "JPY", "1"); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("invalid currency error = %v", err)
	}
	if _, err := service.Quote(context.Background(), "USD", "JPY", "0"); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("invalid amount error = %v", err)
	}
	if _, err := service.Quote(context.Background(), "USD", "ABC", "1"); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Fatalf("unsupported currency error = %v", err)
	}
}

func TestFrankfurterProviderBuildsSnapshotFromAPIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("base"); got != "EUR" {
			t.Errorf("base query = %q, want EUR", got)
		}
		if got := request.URL.Query().Get("providers"); got != "ECB" {
			t.Errorf("providers query = %q, want ECB", got)
		}
		_, _ = writer.Write([]byte(`[{"date":"2026-07-20","base":"EUR","quote":"CNY","rate":7.5},{"date":"2026-07-20","base":"EUR","quote":"JPY","rate":160}]`))
	}))
	defer server.Close()

	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	provider := FrankfurterProvider{Endpoint: server.URL, Base: "EUR", Provider: "ECB", Client: server.Client(), Now: func() time.Time { return now }}
	snapshot, err := provider.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Rates["EUR"] != "1" || snapshot.Rates["CNY"] != "7.5" || snapshot.AsOf != "2026-07-20" {
		t.Fatalf("Fetch() snapshot = %+v", snapshot)
	}
}
