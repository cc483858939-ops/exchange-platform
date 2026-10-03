package translation

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type cancellationProvider func(context.Context, Request) (ProviderResult, error)

func (f cancellationProvider) Translate(ctx context.Context, req Request) (ProviderResult, error) {
	return f(ctx, req)
}

type cancellationCache struct {
	get func(context.Context, string) (string, error)
	set func(context.Context, string, string, time.Duration) error
}

func (c cancellationCache) Get(ctx context.Context, key string) (string, error) {
	if c.get == nil {
		return "", redis.Nil
	}
	return c.get(ctx, key)
}
func (c cancellationCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if c.set == nil {
		return nil
	}
	return c.set(ctx, key, value, ttl)
}

type translationWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *translationWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func awaitTranslation[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("translation did not reach the expected cancellation boundary")
		var zero T
		return zero
	}
}

func callCancellationTranslation(service *TranslationService, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		result, err := service.Translate(ctx, 42, "你好", "zh", "en")
		if err == nil && result.Translation != "hello" {
			err = errors.New("unexpected translation")
		}
		done <- err
	}()
	return done
}

func TestTranslationWaitersHaveIndependentCancellation(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "first-caller", false: "later-deadline"}[cancelFirst], func(t *testing.T) {
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			var calls atomic.Int32
			provider := cancellationProvider(func(ctx context.Context, _ Request) (ProviderResult, error) {
				calls.Add(1)
				started <- ctx
				select {
				case <-release:
					return ProviderResult{Translation: "hello"}, nil
				case <-ctx.Done():
					return ProviderResult{}, ctx.Err()
				}
			})
			service := NewService(provider, nil, ServiceConfig{Enabled: true, WorkTimeout: 3 * time.Second})
			t.Cleanup(service.Close)
			firstCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := callCancellationTranslation(service, firstCtx)
			workCtx := awaitTranslation(t, started)
			secondCtx := context.Background()
			if !cancelFirst {
				var stop context.CancelFunc
				secondCtx, stop = context.WithTimeout(secondCtx, 40*time.Millisecond)
				defer stop()
			}
			waiting := &translationWaitingContext{Context: secondCtx, waiting: make(chan struct{})}
			second := callCancellationTranslation(service, waiting)
			awaitTranslation(t, waiting.waiting) // DoChan has registered the second waiter.
			if cancelFirst {
				cancel()
				if err := awaitTranslation(t, first); !errors.Is(err, context.Canceled) {
					t.Fatalf("first waiter error=%v", err)
				}
			} else if err := awaitTranslation(t, second); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("later waiter error=%v", err)
			}
			if workCtx.Err() != nil {
				t.Fatalf("one waiter canceled shared work: %v", workCtx.Err())
			}
			close(release)
			survivor := first
			if cancelFirst {
				survivor = second
			}
			if err := awaitTranslation(t, survivor); err != nil || calls.Load() != 1 {
				t.Fatalf("survivor error=%v provider calls=%d", err, calls.Load())
			}
		})
	}
}

func TestTranslationSharedWorkBudgetAndClose(t *testing.T) {
	for _, action := range []string{"timeout", "all-waiters-cancel", "close"} {
		t.Run(action, func(t *testing.T) {
			started, stopped := make(chan context.Context, 1), make(chan error, 1)
			provider := cancellationProvider(func(ctx context.Context, _ Request) (ProviderResult, error) {
				started <- ctx
				<-ctx.Done()
				stopped <- ctx.Err()
				return ProviderResult{}, ctx.Err()
			})
			service := NewService(provider, nil, ServiceConfig{Enabled: true, WorkTimeout: 80 * time.Millisecond})
			t.Cleanup(service.Close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := callCancellationTranslation(service, ctx)
			workCtx := awaitTranslation(t, started)
			if _, ok := workCtx.Deadline(); !ok {
				t.Fatal("shared provider work has no deadline")
			}
			if action == "all-waiters-cancel" {
				cancel()
			} else if action == "close" {
				service.Close()
			}
			err := awaitTranslation(t, done)
			if action == "timeout" && !errors.Is(err, ErrProviderTimeout) {
				t.Fatalf("shared timeout error=%v", err)
			}
			if action != "timeout" && !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled/closed waiter error=%v", err)
			}
			wantStop := context.DeadlineExceeded
			if action == "close" {
				wantStop = context.Canceled
			}
			if err := awaitTranslation(t, stopped); !errors.Is(err, wantStop) {
				t.Fatalf("provider stopped with %v want %v", err, wantStop)
			}
		})
	}
}

func TestTranslationCancellationBeforeWorkAndBoundedCacheFallback(t *testing.T) {
	provider := &fakeTranslationProvider{result: ProviderResult{Translation: "hello"}}
	var gets, sets atomic.Int32
	cache := cancellationCache{
		get: func(ctx context.Context, _ string) (string, error) { gets.Add(1); <-ctx.Done(); return "", ctx.Err() },
		set: func(ctx context.Context, _, _ string, _ time.Duration) error {
			sets.Add(1)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	service := NewService(provider, cache, ServiceConfig{Enabled: true, CacheTimeout: 20 * time.Millisecond, WorkTimeout: 300 * time.Millisecond})
	t.Cleanup(service.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Translate(ctx, 42, "你好", "zh", "en"); !errors.Is(err, context.Canceled) || gets.Load() != 0 || provider.callCount() != 0 {
		t.Fatalf("pre-canceled error=%v gets=%d provider=%d", err, gets.Load(), provider.callCount())
	}
	if err := awaitTranslation(t, callCancellationTranslation(service, context.Background())); err != nil || gets.Load() != 2 || sets.Load() != 1 || provider.callCount() != 1 {
		t.Fatalf("fallback error=%v gets=%d sets=%d provider=%d", err, gets.Load(), sets.Load(), provider.callCount())
	}
	service.Close()
	if _, err := service.Translate(context.Background(), 42, "你好", "zh", "en"); !errors.Is(err, context.Canceled) || gets.Load() != 2 {
		t.Fatalf("closed service error=%v gets=%d", err, gets.Load())
	}
}

func TestTranslationRetainsSecondCacheCheckAndBoundedWarmFill(t *testing.T) {
	t.Run("second-lookup", func(t *testing.T) {
		var gets atomic.Int32
		cache := cancellationCache{get: func(context.Context, string) (string, error) {
			if gets.Add(1) == 1 {
				return "", redis.Nil
			}
			return `{"translation":"hello","source_language":"zh","target_language":"en"}`, nil
		}}
		provider := &fakeTranslationProvider{}
		service := NewService(provider, cache, ServiceConfig{Enabled: true})
		defer service.Close()
		if err := awaitTranslation(t, callCancellationTranslation(service, context.Background())); err != nil || provider.callCount() != 0 || gets.Load() != 2 {
			t.Fatalf("second lookup error=%v calls=%d gets=%d", err, provider.callCount(), gets.Load())
		}
	})
	t.Run("warm-after-caller-cancels", func(t *testing.T) {
		started, release, filled := make(chan struct{}), make(chan struct{}), make(chan struct{})
		cache := newFakeTranslationCache()
		provider := &fakeTranslationProvider{started: started, release: release, result: ProviderResult{Translation: "hello"}}
		observed := cancellationCache{get: cache.Get, set: func(ctx context.Context, key, value string, ttl time.Duration) error {
			err := cache.Set(ctx, key, value, ttl)
			close(filled)
			return err
		}}
		service := NewService(provider, observed, ServiceConfig{Enabled: true})
		defer service.Close()
		ctx, cancel := context.WithCancel(context.Background())
		done := callCancellationTranslation(service, ctx)
		awaitTranslation(t, started)
		cancel()
		if err := awaitTranslation(t, done); !errors.Is(err, context.Canceled) {
			t.Fatalf("caller error=%v", err)
		}
		close(release)
		awaitTranslation(t, filled)
		if err := awaitTranslation(t, callCancellationTranslation(service, context.Background())); err != nil || provider.callCount() != 1 {
			t.Fatalf("warm hit error=%v provider=%d", err, provider.callCount())
		}
	})
}

func TestTranslationSharedBudgetIncludesSecondLookupAndBackfill(t *testing.T) {
	for _, stage := range []string{"second-get", "backfill"} {
		t.Run(stage, func(t *testing.T) {
			var gets atomic.Int32
			stopped := make(chan error, 1)
			cache := cancellationCache{
				get: func(ctx context.Context, _ string) (string, error) {
					if gets.Add(1) == 2 && stage == "second-get" {
						<-ctx.Done()
						stopped <- ctx.Err()
						return "", ctx.Err()
					}
					return "", redis.Nil
				},
				set: func(ctx context.Context, _, _ string, _ time.Duration) error {
					<-ctx.Done()
					stopped <- ctx.Err()
					return ctx.Err()
				},
			}
			provider := &fakeTranslationProvider{result: ProviderResult{Translation: "hello"}}
			service := NewService(provider, cache, ServiceConfig{Enabled: true, CacheTimeout: time.Hour, WorkTimeout: 80 * time.Millisecond})
			defer service.Close()
			if err := awaitTranslation(t, callCancellationTranslation(service, context.Background())); !errors.Is(err, ErrProviderTimeout) {
				t.Fatalf("%s shared budget error=%v", stage, err)
			}
			if err := awaitTranslation(t, stopped); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s cache operation error=%v", stage, err)
			}
			wantCalls := 0
			if stage == "backfill" {
				wantCalls = 1
			}
			if provider.callCount() != wantCalls {
				t.Fatalf("provider calls=%d want=%d", provider.callCount(), wantCalls)
			}
		})
	}
}

func newTranslationBlockedRedis(t *testing.T, retries int, readCommand bool) (*redis.Client, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	client := redis.NewClient(&redis.Options{Addr: "pipe", PoolSize: 1, MaxRetries: retries,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		MinRetryBackoff: time.Second, MaxRetryBackoff: time.Second,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				defer serverConn.Close()
				if readCommand {
					reader := bufio.NewReader(serverConn)
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
					if err != nil {
						return
					}
					for index := 0; index < count; index++ {
						line, err = reader.ReadString('\n')
						if err != nil {
							return
						}
						length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
						if err != nil {
							return
						}
						if _, err := io.CopyN(io.Discard, reader, int64(length+2)); err != nil {
							return
						}
					}
				}
				started <- struct{}{}
				<-release
			}()
			return clientConn, nil
		}})
	t.Cleanup(func() { close(release); client.Close() })
	return client, started
}

func TestTranslationRedisCancellationReturnsBeforeIOBudget(t *testing.T) {
	client, started := newTranslationBlockedRedis(t, 2, true)
	redisCache := NewRedisCache(client)
	commandDone := make(chan struct{}, 1)
	cache := cancellationCache{get: func(ctx context.Context, key string) (string, error) {
		value, err := redisCache.Get(ctx, key)
		commandDone <- struct{}{}
		return value, err
	}}
	provider := &fakeTranslationProvider{}
	service := NewService(provider, cache, ServiceConfig{Enabled: true, CacheTimeout: 1500 * time.Millisecond})
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := callCancellationTranslation(service, ctx)
	awaitTranslation(t, started)
	cancel()
	if err := awaitTranslation(t, done); !errors.Is(err, context.Canceled) || provider.callCount() != 0 {
		t.Fatalf("canceled GET error=%v provider=%d", err, provider.callCount())
	}
	// The waiter is already gone; the Redis socket must also be released at
	// its finite deadline, including when retry backoff is enabled.
	select {
	case <-commandDone:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned Redis command did not stop within its I/O budget")
	}
	if stats := client.PoolStats(); stats.TotalConns != 0 {
		t.Fatalf("Redis connection still occupied: %+v", stats)
	}
}

func TestTranslationRedisDeadlinesCoverReadsWritesAndPoolWaits(t *testing.T) {
	for _, operation := range []string{"get", "set-read", "set-write", "pool-wait"} {
		t.Run(operation, func(t *testing.T) {
			client, started := newTranslationBlockedRedis(t, 2, operation != "set-write")
			cache := NewRedisCache(client)
			if operation == "pool-wait" {
				holderCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				go cache.Get(holderCtx, "held")
				awaitTranslation(t, started)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if strings.HasPrefix(operation, "set") {
					err = cache.Set(ctx, "key", "value", time.Hour)
				} else {
					_, err = cache.Get(ctx, "key")
				}
				done <- err
			}()
			if operation != "pool-wait" {
				awaitTranslation(t, started)
			}
			if err := awaitTranslation(t, done); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s error=%v want deadline exceeded", operation, err)
			}
		})
	}
}
