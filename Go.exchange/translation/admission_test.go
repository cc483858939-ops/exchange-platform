package translation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTranslationAdmissionBoundsDifferentKeysAndClosesQueuedWork(t *testing.T) {
	provider := &fakeTranslationProvider{started: make(chan struct{}), release: make(chan struct{}), result: ProviderResult{Translation: "translated"}}
	service := NewService(provider, nil, ServiceConfig{Enabled: true, MaxConcurrent: 1, MaxQueued: 1, WorkTimeout: time.Second})
	t.Cleanup(service.Close)
	first := make(chan error, 1)
	go func() { _, err := service.Translate(t.Context(), 1, "one", "en", "zh"); first <- err }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider never started")
	}
	queued := make(chan error, 1)
	go func() { _, err := service.Translate(t.Context(), 2, "two", "en", "zh"); queued <- err }()
	deadline := time.Now().Add(time.Second)
	for len(service.waiters) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("second key never queued")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := service.Translate(t.Context(), 3, "three", "en", "zh")
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ProviderErrorRateLimited || provider.callCount() != 1 {
		t.Fatalf("overflow err=%v provider calls=%d", err, provider.callCount())
	}
	service.Close()
	for _, result := range []<-chan error{first, queued} {
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("close=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("close stranded shared work")
		}
	}
	deadline = time.Now().Add(time.Second)
	for len(service.slots) != 0 || len(service.waiters) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("close retained admission slots")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTranslationAdmissionKeepsSameKeySingleFlightWhenFull(t *testing.T) {
	release := make(chan struct{})
	provider := &fakeTranslationProvider{started: make(chan struct{}), release: release, result: ProviderResult{Translation: "translated"}}
	service := NewService(provider, nil, ServiceConfig{Enabled: true, MaxConcurrent: 1, MaxQueued: 1, WorkTimeout: time.Second})
	t.Cleanup(service.Close)
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := service.Translate(t.Context(), 1, "same", "en", "zh"); results <- err }()
	}
	<-provider.started
	// Let the second caller join the same flight while the sole slot is occupied.
	time.Sleep(10 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("same-key provider calls=%d", provider.callCount())
	}
}
