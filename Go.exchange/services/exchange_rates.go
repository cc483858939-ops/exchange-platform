package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"golang.org/x/sync/singleflight"
)

var (
	ErrInvalidCurrency     = errors.New("invalid currency")
	ErrInvalidAmount       = errors.New("invalid amount")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrNoRateSnapshot      = errors.New("no usable exchange-rate snapshot")
)

const (
	FreshnessFresh = "fresh"
	FreshnessStale = "stale"
)

type RateSnapshot struct {
	Base      string            `json:"base"`
	Rates     map[string]string `json:"rates"`
	Provider  string            `json:"provider"`
	AsOf      string            `json:"asOf"`
	FetchedAt time.Time         `json:"fetchedAt"`
}

type CurrencyList struct {
	Currencies []string `json:"currencies"`
	AsOf       string   `json:"asOf"`
	Source     string   `json:"source"`
	Freshness  string   `json:"freshness"`
}

type Quote struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Amount          string `json:"amount"`
	Rate            string `json:"rate"`
	ConvertedAmount string `json:"convertedAmount"`
	AsOf            string `json:"asOf"`
	Source          string `json:"source"`
	Freshness       string `json:"freshness"`
}

type SnapshotProvider interface {
	Fetch(context.Context) (RateSnapshot, error)
}

type SnapshotStore interface {
	Load(context.Context) (RateSnapshot, error)
	Save(context.Context, RateSnapshot, time.Duration) error
}

type RateServiceOptions struct {
	FreshFor time.Duration
	MaxStale time.Duration
	Now      func() time.Time
}

type RateService struct {
	provider SnapshotProvider
	store    SnapshotStore
	freshFor time.Duration
	maxStale time.Duration
	now      func() time.Time
	group    singleflight.Group
}

func NewRateService(provider SnapshotProvider, store SnapshotStore, options RateServiceOptions) *RateService {
	freshFor := options.FreshFor
	if freshFor <= 0 {
		freshFor = 30 * time.Minute
	}
	maxStale := options.MaxStale
	if maxStale < freshFor {
		maxStale = 24 * time.Hour
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &RateService{provider: provider, store: store, freshFor: freshFor, maxStale: maxStale, now: now}
}

func (s *RateService) Refresh(ctx context.Context) (RateSnapshot, error) {
	if s.provider == nil || s.store == nil {
		return RateSnapshot{}, ErrNoRateSnapshot
	}
	value, err, _ := s.group.Do("exchange-rate-snapshot", func() (interface{}, error) {
		snapshot, err := s.provider.Fetch(ctx)
		if err != nil {
			return nil, err
		}
		if err := validateSnapshot(snapshot); err != nil {
			return nil, err
		}
		if snapshot.FetchedAt.IsZero() {
			snapshot.FetchedAt = s.now().UTC()
		}
		if err := s.store.Save(ctx, snapshot, s.maxStale); err != nil {
			return nil, fmt.Errorf("save exchange-rate snapshot: %w", err)
		}
		return snapshot, nil
	})
	if err != nil {
		return RateSnapshot{}, err
	}
	return value.(RateSnapshot), nil
}

func (s *RateService) Currencies(ctx context.Context) (CurrencyList, error) {
	snapshot, freshness, err := s.snapshot(ctx)
	if err != nil {
		return CurrencyList{}, err
	}
	currencies := make([]string, 0, len(snapshot.Rates))
	for currency := range snapshot.Rates {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	return CurrencyList{Currencies: currencies, AsOf: snapshot.AsOf, Source: snapshot.Provider, Freshness: freshness}, nil
}

func (s *RateService) Quote(ctx context.Context, from, to, amount string) (Quote, error) {
	from, err := normalizeCurrency(from)
	if err != nil {
		return Quote{}, err
	}
	to, err = normalizeCurrency(to)
	if err != nil {
		return Quote{}, err
	}
	parsedAmount, err := parsePositiveDecimal(amount)
	if err != nil {
		return Quote{}, err
	}
	snapshot, freshness, err := s.snapshot(ctx)
	if err != nil {
		return Quote{}, err
	}
	fromRate, err := snapshotRate(snapshot, from)
	if err != nil {
		return Quote{}, err
	}
	toRate, err := snapshotRate(snapshot, to)
	if err != nil {
		return Quote{}, err
	}
	rate := new(big.Rat).Quo(toRate, fromRate)
	converted := new(big.Rat).Mul(parsedAmount, rate)
	return Quote{
		From:            from,
		To:              to,
		Amount:          formatDecimal(parsedAmount, 6),
		Rate:            formatDecimal(rate, 10),
		ConvertedAmount: formatDecimal(converted, 6),
		AsOf:            snapshot.AsOf,
		Source:          snapshot.Provider,
		Freshness:       freshness,
	}, nil
}

func (s *RateService) snapshot(ctx context.Context) (RateSnapshot, string, error) {
	var cached RateSnapshot
	if s.store != nil {
		if snapshot, err := s.store.Load(ctx); err == nil && validateSnapshot(snapshot) == nil {
			cached = snapshot
			if s.now().Sub(snapshot.FetchedAt) <= s.freshFor {
				return snapshot, FreshnessFresh, nil
			}
		}
	}
	refreshed, err := s.Refresh(ctx)
	if err == nil {
		return refreshed, FreshnessFresh, nil
	}
	if !cached.FetchedAt.IsZero() && s.now().Sub(cached.FetchedAt) <= s.maxStale {
		return cached, FreshnessStale, nil
	}
	return RateSnapshot{}, "", fmt.Errorf("%w: %v", ErrNoRateSnapshot, err)
}

func validateSnapshot(snapshot RateSnapshot) error {
	base, err := normalizeCurrency(snapshot.Base)
	if err != nil {
		return err
	}
	if strings.TrimSpace(snapshot.Provider) == "" || strings.TrimSpace(snapshot.AsOf) == "" || snapshot.FetchedAt.IsZero() || len(snapshot.Rates) == 0 {
		return ErrNoRateSnapshot
	}
	for currency, value := range snapshot.Rates {
		if _, err := normalizeCurrency(currency); err != nil {
			return err
		}
		rate, ok := new(big.Rat).SetString(value)
		if !ok || rate.Sign() <= 0 {
			return ErrNoRateSnapshot
		}
	}
	if _, ok := snapshot.Rates[base]; !ok {
		return ErrNoRateSnapshot
	}
	return nil
}

func snapshotRate(snapshot RateSnapshot, currency string) (*big.Rat, error) {
	raw, ok := snapshot.Rates[currency]
	if !ok {
		return nil, ErrUnsupportedCurrency
	}
	rate, ok := new(big.Rat).SetString(raw)
	if !ok || rate.Sign() <= 0 {
		return nil, ErrNoRateSnapshot
	}
	return rate, nil
}

func normalizeCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if len(currency) != 3 {
		return "", ErrInvalidCurrency
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			return "", ErrInvalidCurrency
		}
	}
	return currency, nil
}

func parsePositiveDecimal(raw string) (*big.Rat, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 64 {
		return nil, ErrInvalidAmount
	}
	digits := 0
	dotSeen := false
	for _, char := range value {
		switch {
		case char >= '0' && char <= '9':
			digits++
		case char == '.' && !dotSeen:
			dotSeen = true
		default:
			return nil, ErrInvalidAmount
		}
	}
	if digits == 0 {
		return nil, ErrInvalidAmount
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok || parsed.Sign() <= 0 {
		return nil, ErrInvalidAmount
	}
	return parsed, nil
}

func formatDecimal(value *big.Rat, scale int) string {
	formatted := strings.TrimRight(strings.TrimRight(value.FloatString(scale), "0"), ".")
	if formatted == "" || formatted == "-0" {
		return "0"
	}
	return formatted
}

type RedisSnapshotStore struct {
	Client *redis.Client
	Key    string
}

func (s RedisSnapshotStore) Load(_ context.Context) (RateSnapshot, error) {
	if s.Client == nil {
		return RateSnapshot{}, ErrNoRateSnapshot
	}
	raw, err := s.Client.Get(s.key()).Result()
	if err == redis.Nil {
		return RateSnapshot{}, ErrNoRateSnapshot
	}
	if err != nil {
		return RateSnapshot{}, fmt.Errorf("load exchange-rate snapshot: %w", err)
	}
	var snapshot RateSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return RateSnapshot{}, fmt.Errorf("decode exchange-rate snapshot: %w", err)
	}
	return snapshot, nil
}

func (s RedisSnapshotStore) Save(_ context.Context, snapshot RateSnapshot, ttl time.Duration) error {
	if s.Client == nil {
		return ErrNoRateSnapshot
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode exchange-rate snapshot: %w", err)
	}
	return s.Client.Set(s.key(), raw, ttl).Err()
}

func (s RedisSnapshotStore) key() string {
	if strings.TrimSpace(s.Key) != "" {
		return s.Key
	}
	return consts.ExchangeRateSnapshotKey
}

type FrankfurterProvider struct {
	Endpoint string
	Base     string
	Provider string
	Client   *http.Client
	Now      func() time.Time
}

type frankfurterRate struct {
	Date  string      `json:"date"`
	Base  string      `json:"base"`
	Quote string      `json:"quote"`
	Rate  json.Number `json:"rate"`
}

func (p FrankfurterProvider) Fetch(ctx context.Context) (RateSnapshot, error) {
	base, err := normalizeCurrency(p.Base)
	if err != nil {
		return RateSnapshot{}, fmt.Errorf("configure base currency: %w", err)
	}
	endpoint, err := url.Parse(strings.TrimSpace(p.Endpoint))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return RateSnapshot{}, errors.New("configure exchange-rate endpoint")
	}
	query := endpoint.Query()
	query.Set("base", base)
	if provider := strings.TrimSpace(p.Provider); provider != "" {
		query.Set("providers", provider)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return RateSnapshot{}, fmt.Errorf("create exchange-rate request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return RateSnapshot{}, fmt.Errorf("fetch exchange rates: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RateSnapshot{}, fmt.Errorf("fetch exchange rates: upstream returned %d", response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	var records []frankfurterRate
	if err := decoder.Decode(&records); err != nil {
		return RateSnapshot{}, fmt.Errorf("decode exchange rates: %w", err)
	}
	if len(records) == 0 {
		return RateSnapshot{}, errors.New("exchange-rate provider returned no rates")
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	snapshot := RateSnapshot{
		Base:      base,
		Rates:     map[string]string{base: "1"},
		Provider:  "Frankfurter / " + strings.ToUpper(strings.TrimSpace(p.Provider)),
		FetchedAt: now().UTC(),
	}
	if strings.TrimSpace(p.Provider) == "" {
		snapshot.Provider = "Frankfurter"
	}
	for _, record := range records {
		if recordBase, err := normalizeCurrency(record.Base); err != nil || recordBase != base {
			return RateSnapshot{}, errors.New("exchange-rate provider returned an unexpected base currency")
		}
		quote, err := normalizeCurrency(record.Quote)
		if err != nil {
			return RateSnapshot{}, errors.New("exchange-rate provider returned an invalid quote currency")
		}
		if snapshot.AsOf == "" {
			snapshot.AsOf = record.Date
		} else if snapshot.AsOf != record.Date {
			return RateSnapshot{}, errors.New("exchange-rate provider returned mixed quote dates")
		}
		rate, ok := new(big.Rat).SetString(record.Rate.String())
		if !ok || rate.Sign() <= 0 {
			return RateSnapshot{}, errors.New("exchange-rate provider returned an invalid rate")
		}
		snapshot.Rates[quote] = record.Rate.String()
	}
	return snapshot, nil
}

var (
	defaultRateServiceOnce sync.Once
	defaultRateService     *RateService
)

func DefaultExchangeRateService() *RateService {
	defaultRateServiceOnce.Do(func() {
		provider := FrankfurterProvider{
			Endpoint: config.ExchangeRateEndpoint(),
			Base:     config.ExchangeRateBaseCurrency(),
			Provider: config.ExchangeRateProvider(),
			Client:   &http.Client{Timeout: config.ExchangeRateRequestTimeout()},
		}
		defaultRateService = NewRateService(
			provider,
			RedisSnapshotStore{Client: global.RedisDB},
			RateServiceOptions{FreshFor: config.ExchangeRateFreshFor(), MaxStale: config.ExchangeRateMaxStale()},
		)
	})
	return defaultRateService
}
