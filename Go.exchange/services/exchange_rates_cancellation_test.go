package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rateWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *rateWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func awaitRate[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("rate service did not reach the expected cancellation boundary")
		var zero T
		return zero
	}
}

func callCancellationRate(service *RateService, ctx context.Context, refresh bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		var err error
		if refresh {
			_, err = service.Refresh(ctx)
		} else {
			_, err = service.Currencies(ctx)
		}
		done <- err
	}()
	return done
}

func TestRateSharedRefreshWaitersCancelIndependently(t *testing.T) {
	for _, variant := range []string{"first-caller", "later-deadline", "save-stage"} {
		t.Run(variant, func(t *testing.T) {
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			var calls, saves atomic.Int32
			provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) {
				calls.Add(1)
				if variant != "save-stage" {
					started <- ctx
					select {
					case <-release:
					case <-ctx.Done():
						return RateSnapshot{}, ctx.Err()
					}
				}
				return sampleSnapshot(time.Now()), nil
			})
			store := functionSnapshotStore{
				load: func(context.Context) (RateSnapshot, error) { return RateSnapshot{}, ErrNoRateSnapshot },
				save: func(ctx context.Context, _ RateSnapshot, _ time.Duration) error {
					saves.Add(1)
					if variant == "save-stage" {
						started <- ctx
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				},
			}
			service := NewRateService(provider, store, RateServiceOptions{RefreshTimeout: 3 * time.Second})
			t.Cleanup(service.Close)
			firstCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := callCancellationRate(service, firstCtx, false)
			workCtx := awaitRate(t, started)
			secondCtx := context.Background()
			if variant == "later-deadline" {
				var stop context.CancelFunc
				secondCtx, stop = context.WithTimeout(secondCtx, 40*time.Millisecond)
				defer stop()
			}
			waiting := &rateWaitingContext{Context: secondCtx, waiting: make(chan struct{})}
			second := callCancellationRate(service, waiting, false)
			awaitRate(t, waiting.waiting)
			if variant == "later-deadline" {
				if err := awaitRate(t, second); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("later waiter error=%v", err)
				}
			} else {
				cancel()
				if err := awaitRate(t, first); !errors.Is(err, context.Canceled) {
					t.Fatalf("first waiter error=%v", err)
				}
			}
			if workCtx.Err() != nil {
				t.Fatalf("caller canceled shared %s work: %v", variant, workCtx.Err())
			}
			close(release)
			survivor := second
			if variant == "later-deadline" {
				survivor = first
			}
			if err := awaitRate(t, survivor); err != nil || calls.Load() != 1 || saves.Load() != 1 {
				t.Fatalf("survivor error=%v fetch=%d save=%d", err, calls.Load(), saves.Load())
			}
		})
	}
}

func TestRateSharedRefreshBudgetCloseAndAllWaitersExit(t *testing.T) {
	for _, variant := range []string{"fetch-timeout", "save-timeout", "all-waiters-cancel", "close"} {
		t.Run(variant, func(t *testing.T) {
			started, stopped := make(chan context.Context, 1), make(chan error, 1)
			var fetchDeadline time.Time
			provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) {
				fetchDeadline, _ = ctx.Deadline()
				if variant == "save-timeout" {
					return sampleSnapshot(time.Now()), nil
				}
				started <- ctx
				<-ctx.Done()
				stopped <- ctx.Err()
				return RateSnapshot{}, ctx.Err()
			})
			store := functionSnapshotStore{save: func(ctx context.Context, _ RateSnapshot, _ time.Duration) error {
				if deadline, ok := ctx.Deadline(); !ok || deadline != fetchDeadline {
					t.Error("Save was given a new budget instead of the remaining Fetch budget")
				}
				started <- ctx
				<-ctx.Done()
				stopped <- ctx.Err()
				return ctx.Err()
			}}
			service := NewRateService(provider, store, RateServiceOptions{RefreshTimeout: 80 * time.Millisecond})
			t.Cleanup(service.Close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := callCancellationRate(service, ctx, true)
			workCtx := awaitRate(t, started)
			if _, ok := workCtx.Deadline(); !ok {
				t.Fatal("shared refresh has no deadline")
			}
			if variant == "all-waiters-cancel" {
				cancel()
			}
			if variant == "close" {
				service.Close()
			}
			err := awaitRate(t, done)
			want := context.DeadlineExceeded
			if variant == "all-waiters-cancel" || variant == "close" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("waiter error=%v want %v", err, want)
			}
			wantStop := context.DeadlineExceeded
			if variant == "close" {
				wantStop = context.Canceled
			}
			if err := awaitRate(t, stopped); !errors.Is(err, wantStop) {
				t.Fatalf("work error=%v want %v", err, wantStop)
			}
		})
	}
}

func TestRateSharedTimeoutPreservesStaleAndColdCacheSemantics(t *testing.T) {
	for _, hasStale := range []bool{true, false} {
		t.Run(map[bool]string{true: "stale", false: "cold"}[hasStale], func(t *testing.T) {
			now := time.Now()
			provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) { <-ctx.Done(); return RateSnapshot{}, ctx.Err() })
			store := &memorySnapshotStore{snapshot: sampleSnapshot(now.Add(-time.Hour)), hasValue: hasStale}
			service := NewRateService(provider, store, RateServiceOptions{RefreshTimeout: 40 * time.Millisecond})
			defer service.Close()
			list, err := service.Currencies(context.Background())
			if hasStale {
				if err != nil || list.Freshness != FreshnessStale {
					t.Fatalf("stale=%+v error=%v", list, err)
				}
			} else if !errors.Is(err, ErrNoRateSnapshot) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cold cache error=%v", err)
			}
		})
	}
}

func TestRateRefreshCanceledBeforeStartDoesNotFetch(t *testing.T) {
	var calls atomic.Int32
	service := NewRateService(rateProviderFunc(func(context.Context) (RateSnapshot, error) { calls.Add(1); return sampleSnapshot(time.Now()), nil }), &memorySnapshotStore{}, RateServiceOptions{})
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Refresh(ctx); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("pre-canceled error=%v calls=%d", err, calls.Load())
	}
	service.Close()
	if _, err := service.Refresh(context.Background()); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("closed error=%v calls=%d", err, calls.Load())
	}
	if _, err := service.Currencies(context.Background()); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("closed currencies error=%v calls=%d", err, calls.Load())
	}
}

func TestRateSharedRefreshMayWarmCacheAfterAllCallersExit(t *testing.T) {
	started, release, saved := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	provider := rateProviderFunc(func(ctx context.Context) (RateSnapshot, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return sampleSnapshot(time.Now()), nil
		case <-ctx.Done():
			return RateSnapshot{}, ctx.Err()
		}
	})
	cache := &memorySnapshotStore{}
	store := functionSnapshotStore{load: cache.Load, save: func(ctx context.Context, snapshot RateSnapshot, ttl time.Duration) error {
		err := cache.Save(ctx, snapshot, ttl)
		close(saved)
		return err
	}}
	service := NewRateService(provider, store, RateServiceOptions{RefreshTimeout: 3 * time.Second})
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := callCancellationRate(service, ctx, false)
	awaitRate(t, started)
	cancel()
	if err := awaitRate(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error=%v", err)
	}
	close(release)
	awaitRate(t, saved)
	list, err := service.Currencies(context.Background())
	if err != nil || list.Freshness != FreshnessFresh || calls.Load() != 1 {
		t.Fatalf("warm cache list=%+v error=%v calls=%d", list, err, calls.Load())
	}
}
