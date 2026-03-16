package tasks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"Go.exchange/metrics"
)

func TestCheckAndScaleUpdatesDirtyBacklogMetric(t *testing.T) {
	originalDirtyBacklogCount := dirtyBacklogCount
	dirtyBacklogCount = func() (int64, error) {
		return 123, nil
	}
	t.Cleanup(func() {
		dirtyBacklogCount = originalDirtyBacklogCount
		metrics.SetDirtyBacklog(0)
	})

	drainSemaphore()

	var wg sync.WaitGroup
	checkAndScale(context.Background(), &wg)
	waitForWorkers(&wg)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status: got %d want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "go_exchange_tasks_dirty_backlog 123") {
		t.Fatalf("expected dirty backlog gauge in metrics output, got:\n%s", recorder.Body.String())
	}
}

func TestDynamicWorkerMetricReflectsSemaphoreLength(t *testing.T) {
	drainSemaphore()
	t.Cleanup(func() {
		drainSemaphore()
	})

	for i := 0; i < 3; i++ {
		sem <- struct{}{}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status: got %d want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "go_exchange_tasks_dynamic_workers 3") {
		t.Fatalf("expected dynamic worker gauge in metrics output, got:\n%s", recorder.Body.String())
	}
}

func drainSemaphore() {
	for {
		select {
		case <-sem:
		default:
			return
		}
	}
}

func waitForWorkers(wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	<-done
}
