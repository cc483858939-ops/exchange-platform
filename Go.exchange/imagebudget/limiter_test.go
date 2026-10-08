package imagebudget

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterBoundsActivePixelsAndQueueWithCancellation(t *testing.T) {
	limiter := New(2, 1, 100, time.Second)
	release, err := limiter.Acquire(t.Context(), 70)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		done, err := limiter.Acquire(ctx, 40)
		if done != nil {
			done()
		}
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		limiter.mu.Lock()
		waiting := limiter.waiting
		limiter.mu.Unlock()
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pixel-bound request never queued")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := limiter.Acquire(t.Context(), 40); !errors.Is(err, ErrBusy) {
		t.Fatalf("queue overflow=%v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation=%v", err)
	}
	release()
	release()
	limiter.mu.Lock()
	active, waiting, pixels := limiter.active, limiter.waiting, limiter.pixels
	limiter.mu.Unlock()
	if active != 0 || waiting != 0 || pixels != 0 {
		t.Fatalf("budget leaked: active=%d waiting=%d pixels=%d", active, waiting, pixels)
	}
	if _, err := limiter.Acquire(t.Context(), 101); !errors.Is(err, ErrBusy) {
		t.Fatalf("oversized weight=%v", err)
	}
	second, err := limiter.Acquire(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestLimiterConcurrentReleaseNeverExceedsCapacity(t *testing.T) {
	limiter := New(2, 20, 100, time.Second)
	var workers sync.WaitGroup
	var active, peak atomic.Int64
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			release, err := limiter.Acquire(t.Context(), 50)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer release()
			current := active.Add(1)
			for {
				old := peak.Load()
				if current <= old || peak.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		}()
	}
	workers.Wait()
	if peak.Load() > 2 || peak.Load() == 0 {
		t.Fatalf("active work peak=%d", peak.Load())
	}
}

func TestLimiterWaitingHasOwnDeadline(t *testing.T) {
	limiter := New(1, 1, 100, 20*time.Millisecond)
	release, err := limiter.Acquire(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := limiter.Acquire(t.Context(), 1); !errors.Is(err, ErrBusy) {
		t.Fatalf("wait deadline=%v", err)
	}
	limiter.mu.Lock()
	waiting := limiter.waiting
	limiter.mu.Unlock()
	if waiting != 0 {
		t.Fatal("timed-out request retained a queue slot")
	}
}
