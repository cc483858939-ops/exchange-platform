package translation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type fakeTranslationProvider struct {
	mu       sync.Mutex
	calls    int
	requests []Request
	result   ProviderResult
	err      error
	started  chan struct{}
	release  <-chan struct{}
	once     sync.Once
}

func (p *fakeTranslationProvider) Translate(ctx context.Context, request Request) (ProviderResult, error) {
	p.mu.Lock()
	p.calls++
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.started != nil {
		p.once.Do(func() { close(p.started) })
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return ProviderResult{}, ctx.Err()
		}
	}
	return p.result, p.err
}

func (p *fakeTranslationProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type fakeTranslationCache struct {
	mu          sync.Mutex
	values      map[string]string
	getErr      error
	setErr      error
	getCalls    int
	setCalls    int
	expirations []time.Duration
}

func newFakeTranslationCache() *fakeTranslationCache {
	return &fakeTranslationCache{values: make(map[string]string)}
}

func (c *fakeTranslationCache) Get(key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	if c.getErr != nil {
		return "", c.getErr
	}
	value, ok := c.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (c *fakeTranslationCache) Set(key, value string, expiration time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	c.expirations = append(c.expirations, expiration)
	if c.setErr != nil {
		return c.setErr
	}
	c.values[key] = value
	return nil
}

func TestTranslationServiceReturnsOriginalForSameLanguageWithoutDependencies(t *testing.T) {
	service := NewService(nil, nil, ServiceConfig{Enabled: true})

	result, err := service.Translate(context.Background(), 42, "Already English", "EN", "en")
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if result.Translation != "Already English" || result.SourceLanguage != "en" || !(!result.Translated) {
		t.Fatalf("result = %+v", result)
	}
}

func TestTranslationServiceRejectsOversizedUnicodeSource(t *testing.T) {
	provider := &fakeTranslationProvider{result: ProviderResult{Translation: "unused"}}
	service := NewService(provider, nil, ServiceConfig{
		Enabled:        true,
		MaxSourceRunes: 2,
	})

	_, err := service.Translate(context.Background(), 42, "你🙂好", "zh", "en")
	if !errors.Is(err, ErrSourceTooLong) {
		t.Fatalf("error = %v, want %v", err, ErrSourceTooLong)
	}
	if provider.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.callCount())
	}
}

func TestTranslationServiceCachesGeneratedResultAndUsesBoundedJitter(t *testing.T) {
	provider := &fakeTranslationProvider{result: ProviderResult{Translation: "  hello\nworld  "}}
	cache := newFakeTranslationCache()
	baseTTL := 7 * 24 * time.Hour
	jitterMax := 24 * time.Hour
	service := NewService(provider, cache, ServiceConfig{
		Enabled:       true,
		Model:         "model-a",
		PromptVersion: "social_v1",
		BaseTTL:       baseTTL,
		CacheJitter:   jitterMax,
		Jitter:        func(max time.Duration) time.Duration { return max / 2 },
	})

	first, err := service.Translate(context.Background(), 42, "你好", "zh", "en")
	if err != nil {
		t.Fatalf("first Translate() error = %v", err)
	}
	second, err := service.Translate(context.Background(), 42, "你好", "zh", "en")
	if err != nil {
		t.Fatalf("second Translate() error = %v", err)
	}
	if first.Translation != "hello\nworld" || second.Translation != first.Translation {
		t.Fatalf("results = %+v, %+v", first, second)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
	if len(cache.expirations) != 1 || cache.expirations[0] != baseTTL+jitterMax/2 {
		t.Fatalf("cache expirations = %v", cache.expirations)
	}
}

func TestTranslationServiceTreatsRedisFailuresAsNonFatal(t *testing.T) {
	provider := &fakeTranslationProvider{result: ProviderResult{Translation: "hello"}}
	cache := newFakeTranslationCache()
	cache.getErr = errors.New("redis get failed")
	cache.setErr = errors.New("redis set failed")
	service := NewService(provider, cache, ServiceConfig{Enabled: true})

	result, err := service.Translate(context.Background(), 42, "你好", "zh", "en")
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if result.Translation != "hello" {
		t.Fatalf("translation = %q", result.Translation)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
}

func TestTranslationServiceCoalescesConcurrentProviderCalls(t *testing.T) {
	release := make(chan struct{})
	provider := &fakeTranslationProvider{
		result:  ProviderResult{Translation: "hello"},
		started: make(chan struct{}),
		release: release,
	}
	cache := newFakeTranslationCache()
	service := NewService(provider, cache, ServiceConfig{Enabled: true})

	const callers = 12
	results := make(chan Result, callers)
	errorsOut := make(chan error, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for range callers {
		go func() {
			defer waitGroup.Done()
			result, err := service.Translate(context.Background(), 42, "你好", "zh", "en")
			results <- result
			errorsOut <- err
		}()
	}

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	close(release)
	waitGroup.Wait()
	close(results)
	close(errorsOut)

	for err := range errorsOut {
		if err != nil {
			t.Fatalf("coalesced request error = %v", err)
		}
	}
	for result := range results {
		if result.Translation != "hello" {
			t.Fatalf("coalesced translation = %q", result.Translation)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
}
