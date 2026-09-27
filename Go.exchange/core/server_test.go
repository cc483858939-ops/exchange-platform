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
)

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
