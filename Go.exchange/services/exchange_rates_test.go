package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type stubRateProvider struct {
	snapshot RateSnapshot
	err      error
}

func (p stubRateProvider) Fetch(context.Context) (RateSnapshot, error) {
	return p.snapshot, p.err
}

type rateProviderFunc func(context.Context) (RateSnapshot, error)

func (f rateProviderFunc) Fetch(ctx context.Context) (RateSnapshot, error) {
	return f(ctx)
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

func TestRateServiceUsesStaleSnapshotWhenProviderTimesOutIndependently(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	store := &memorySnapshotStore{snapshot: sampleSnapshot(now.Add(-time.Hour)), hasValue: true}
	service := NewRateService(
		stubRateProvider{err: fmt.Errorf("fetch exchange rates: %w", context.DeadlineExceeded)},
		store,
		RateServiceOptions{FreshFor: 30 * time.Minute, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	quote, err := service.Quote(context.Background(), "EUR", "USD", "2")
	if err != nil {
		t.Fatalf("Quote() error = %v, want stale snapshot after provider-local timeout", err)
	}
	if quote.Freshness != FreshnessStale || quote.ConvertedAmount != "2.5" {
		t.Fatalf("Quote() = %+v, want stale 2.5", quote)
	}
}

func TestRateServiceDoesNotUseStaleSnapshotAfterCallerCancellation(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &memorySnapshotStore{snapshot: sampleSnapshot(now.Add(-time.Hour)), hasValue: true}
	providerCalls := 0
	provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) {
		providerCalls++
		cancel()
		return RateSnapshot{}, ctx.Err()
	})
	service := NewRateService(
		provider,
		store,
		RateServiceOptions{FreshFor: 30 * time.Minute, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	_, err := service.Quote(ctx, "EUR", "USD", "2")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Quote() error = %v, want context.Canceled", err)
	}
	if providerCalls != 1 {
		t.Fatalf("provider Fetch() calls = %d, want 1", providerCalls)
	}
}

func TestRateServiceDoesNotUseStaleSnapshotAfterCallerDeadline(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	store := &memorySnapshotStore{snapshot: sampleSnapshot(now.Add(-time.Hour)), hasValue: true}
	provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) {
		<-ctx.Done()
		return RateSnapshot{}, ctx.Err()
	})
	service := NewRateService(
		provider,
		store,
		RateServiceOptions{FreshFor: 30 * time.Minute, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	_, err := service.Quote(ctx, "EUR", "USD", "2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Quote() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRateServiceReturnsNoSnapshotWhenProviderTimesOutWithoutCache(t *testing.T) {
	service := NewRateService(
		stubRateProvider{err: fmt.Errorf("fetch exchange rates: %w", context.DeadlineExceeded)},
		&memorySnapshotStore{},
		RateServiceOptions{},
	)

	_, err := service.Quote(context.Background(), "EUR", "USD", "2")
	if !errors.Is(err, ErrNoRateSnapshot) {
		t.Fatalf("Quote() error = %v, want ErrNoRateSnapshot", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Quote() error = %v, provider-local timeout must not be returned as caller deadline", err)
	}
}

func TestRateServicePropagatesSnapshotLoadCancellation(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := functionSnapshotStore{
		load: func(ctx context.Context) (RateSnapshot, error) {
			cancel()
			return sampleSnapshot(now.Add(-time.Hour)), fmt.Errorf("load snapshot: %w", ctx.Err())
		},
	}
	provider := &countingRateProvider{snapshot: sampleSnapshot(now)}
	service := NewRateService(
		provider,
		store,
		RateServiceOptions{FreshFor: 30 * time.Minute, MaxStale: 24 * time.Hour, Now: func() time.Time { return now }},
	)

	if _, err := service.Quote(ctx, "EUR", "USD", "2"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Quote() error = %v, want context.Canceled", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider Fetch() calls = %d, want 0 after snapshot load cancellation", provider.calls)
	}
}

func TestRateServiceRefreshPreservesSnapshotSaveCancellation(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	store := functionSnapshotStore{
		load: func(context.Context) (RateSnapshot, error) {
			return RateSnapshot{}, ErrNoRateSnapshot
		},
		save: func(context.Context, RateSnapshot, time.Duration) error {
			return fmt.Errorf("redis write: %w", context.DeadlineExceeded)
		},
	}
	service := NewRateService(
		stubRateProvider{snapshot: sampleSnapshot(now)},
		store,
		RateServiceOptions{Now: func() time.Time { return now }},
	)

	if _, err := service.Refresh(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Refresh() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRedisSnapshotStoreLoadRejectsCancelledContext(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	store := RedisSnapshotStore{Client: client}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
}

func TestRedisSnapshotStoreSaveRejectsCancelledContext(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	store := RedisSnapshotStore{Client: client}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Save(ctx, sampleSnapshot(time.Now()), time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save() error = %v, want context.Canceled", err)
	}
}

func TestRedisSnapshotStoreRejectsNilContext(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	store := RedisSnapshotStore{Client: client}

	if _, err := store.Load(nil); err == nil {
		t.Fatal("Load(nil) error = nil, want a context error")
	}
	if err := store.Save(nil, sampleSnapshot(time.Now()), time.Hour); err == nil {
		t.Fatal("Save(nil) error = nil, want a context error")
	}
}

type functionSnapshotStore struct {
	load func(context.Context) (RateSnapshot, error)
	save func(context.Context, RateSnapshot, time.Duration) error
}

func (s functionSnapshotStore) Load(ctx context.Context) (RateSnapshot, error) {
	return s.load(ctx)
}

func (s functionSnapshotStore) Save(ctx context.Context, snapshot RateSnapshot, ttl time.Duration) error {
	if s.save == nil {
		return nil
	}
	return s.save(ctx, snapshot, ttl)
}

type countingRateProvider struct {
	snapshot RateSnapshot
	calls    int
}

func (p *countingRateProvider) Fetch(context.Context) (RateSnapshot, error) {
	p.calls++
	return p.snapshot, nil
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
