package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/recommendation"
	"Go.exchange/services"
	"Go.exchange/translation"
)

type shutdownTranslationProvider struct {
	started chan struct{}
	stopped chan struct{}
}

func (p shutdownTranslationProvider) Translate(ctx context.Context, _ translation.Request) (translation.ProviderResult, error) {
	close(p.started)
	<-ctx.Done()
	close(p.stopped)
	return translation.ProviderResult{}, ctx.Err()
}

type shutdownRateProvider struct {
	started chan struct{}
	stopped chan struct{}
}

func (p shutdownRateProvider) Fetch(ctx context.Context) (services.RateSnapshot, error) {
	close(p.started)
	<-ctx.Done()
	close(p.stopped)
	return services.RateSnapshot{}, ctx.Err()
}

type shutdownRateStore struct{}

func (shutdownRateStore) Load(context.Context) (services.RateSnapshot, error) {
	return services.RateSnapshot{}, services.ErrNoRateSnapshot
}
func (shutdownRateStore) Save(ctx context.Context, _ services.RateSnapshot, _ time.Duration) error {
	return ctx.Err()
}

func TestAPIRuntimeShutdownCancelsSharedExternalWork(t *testing.T) {
	translationStarted, translationStopped := make(chan struct{}), make(chan struct{})
	rateStarted, rateStopped := make(chan struct{}), make(chan struct{})
	translationService := translation.NewService(shutdownTranslationProvider{translationStarted, translationStopped}, nil,
		translation.ServiceConfig{Enabled: true, WorkTimeout: time.Hour})
	rateService := services.NewRateService(shutdownRateProvider{rateStarted, rateStopped}, shutdownRateStore{},
		services.RateServiceOptions{RefreshTimeout: time.Hour})
	t.Cleanup(translationService.Close)
	t.Cleanup(rateService.Close)
	callers := make(chan struct{}, 2)
	go func() {
		translationService.Translate(context.Background(), 42, "你好", "zh", "en")
		callers <- struct{}{}
	}()
	go func() { rateService.Refresh(context.Background()); callers <- struct{}{} }()
	await := func(channel <-chan struct{}) {
		t.Helper()
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatal("shared external work did not reach its shutdown boundary")
		}
	}
	await(translationStarted)
	await(rateStarted)
	shutdownAPIRuntime(nil, &APIRuntime{translationService: translationService, rateService: rateService}, nil)
	await(translationStopped)
	await(rateStopped)
	await(callers)
	await(callers)
}

func TestNewAPIServerHasResourceTimeouts(t *testing.T) {
	server := newAPIServer(":3000", http.NotFoundHandler())
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s, want %s", server.ReadHeaderTimeout, 5*time.Second)
	}
	if server.ReadTimeout != 60*time.Second {
		t.Fatalf("ReadTimeout=%s, want %s", server.ReadTimeout, 60*time.Second)
	}
	if server.WriteTimeout != 120*time.Second {
		t.Fatalf("WriteTimeout=%s, want %s", server.WriteTimeout, 120*time.Second)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout=%s, want %s", server.IdleTimeout, 120*time.Second)
	}
	if server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes=%d, want %d", server.MaxHeaderBytes, 1<<20)
	}
}

type shutdownOrderTraceDispatcher struct {
	activeAtShutdown chan int32
	activeHandlers   *atomic.Int32
}

func (dispatcher *shutdownOrderTraceDispatcher) TryEnqueue(recommendation.TracePersistJob) recommendation.TraceEnqueueResult {
	return recommendation.TraceEnqueueDroppedStopping
}

func (dispatcher *shutdownOrderTraceDispatcher) Shutdown(ctx context.Context) error {
	select {
	case dispatcher.activeAtShutdown <- dispatcher.activeHandlers.Load():
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func TestAPIRuntimeDrainsTraceDispatcherAfterHTTPHandlersFinish(t *testing.T) {
	var activeHandlers atomic.Int32
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		activeHandlers.Add(1)
		defer activeHandlers.Add(-1)
		close(handlerStarted)
		<-releaseHandler
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	defer unblock()
	dispatcher := &shutdownOrderTraceDispatcher{
		activeAtShutdown: make(chan int32, 1),
		activeHandlers:   &activeHandlers,
	}
	runtime := &APIRuntime{Server: server.Config, traceDispatcher: dispatcher, traceDrainTimeout: time.Second}
	responseDone := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL)
		if err == nil {
			response.Body.Close()
		}
		responseDone <- err
	}()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler did not start")
	}

	shutdownDone := make(chan struct{})
	go func() {
		shutdownAPIRuntime(nil, runtime, nil)
		close(shutdownDone)
	}()
	select {
	case count := <-dispatcher.activeAtShutdown:
		t.Fatalf("dispatcher shutdown ran while %d HTTP handlers remained active", count)
	case <-time.After(50 * time.Millisecond):
	}

	unblock()
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatalf("HTTP request during shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active HTTP handler did not finish")
	}
	select {
	case count := <-dispatcher.activeAtShutdown:
		if count != 0 {
			t.Fatalf("dispatcher shutdown observed %d active handlers", count)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher shutdown was not reached")
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("API runtime shutdown did not finish")
	}
}
