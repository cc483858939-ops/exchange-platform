package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/runtimehealth"
)

func TestNewWorkerHealthServerHasResourceTimeouts(t *testing.T) {
	server := newWorkerHealthServer(":8081", http.NotFoundHandler())
	if server.ReadHeaderTimeout != 3*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s, want %s", server.ReadHeaderTimeout, 3*time.Second)
	}
	if server.ReadTimeout != 5*time.Second {
		t.Fatalf("ReadTimeout=%s, want %s", server.ReadTimeout, 5*time.Second)
	}
	if server.WriteTimeout != 10*time.Second {
		t.Fatalf("WriteTimeout=%s, want %s", server.WriteTimeout, 10*time.Second)
	}
	if server.IdleTimeout != 30*time.Second {
		t.Fatalf("IdleTimeout=%s, want %s", server.IdleTimeout, 30*time.Second)
	}
	if server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes=%d, want %d", server.MaxHeaderBytes, 64<<10)
	}
}

func TestWorkerHealthzDoesNotCallSnapshotProvider(t *testing.T) {
	calls := 0
	handler := workerHealthHandler(func() runtimehealth.WorkerReadinessSnapshot {
		calls++
		return runtimehealth.WorkerReadinessSnapshot{Status: "ready", Role: "worker"}
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if calls != 0 {
		t.Fatalf("healthz called snapshot provider %d times", calls)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store cache header: %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %q", response.Header().Get("Content-Type"))
	}
}

func TestWorkerReadyzNilProviderFailsClosed(t *testing.T) {
	response := httptest.NewRecorder()
	workerHealthHandler(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
	var body runtimehealth.WorkerReadinessSnapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if body.Status != "not_ready" || body.Role != "worker" || len(body.ReasonCodes) != 1 || body.ReasonCodes[0] != "worker_readiness_unavailable" {
		t.Fatalf("unexpected readiness response: %+v", body)
	}
}

func TestWorkerReadyzOnlyExactReadyStatusReturnsOK(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		code   int
	}{
		{name: "ready", status: "ready", code: http.StatusOK},
		{name: "unknown", status: "READY", code: http.StatusServiceUnavailable},
		{name: "empty", status: "", code: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			workerHealthHandler(func() runtimehealth.WorkerReadinessSnapshot {
				return runtimehealth.WorkerReadinessSnapshot{Status: test.status, Role: "worker"}
			}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != test.code {
				t.Fatalf("expected %d, got %d", test.code, response.Code)
			}
		})
	}
}
