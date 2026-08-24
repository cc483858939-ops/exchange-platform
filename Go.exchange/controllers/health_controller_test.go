package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/runtimehealth"

	"github.com/gin-gonic/gin"
)

type healthReadinessStub struct{ snapshot runtimehealth.APISnapshot }

func (stub healthReadinessStub) Snapshot() runtimehealth.APISnapshot { return stub.snapshot }

func TestHealthzIsPureLiveness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/healthz", Healthz)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store cache header: %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %q", response.Header().Get("Content-Type"))
	}
	if response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("unexpected health response: %s", response.Body.String())
	}
}

func TestReadyzWithNilProviderFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/readyz", ReadyzWithProvider(nil))

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store cache header: %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %q", response.Header().Get("Content-Type"))
	}
	if response.Body.String() != `{"status":"not_ready","role":"api","checks":{},"reason_codes":["readiness_provider_unavailable"]}` {
		t.Fatalf("unexpected readiness response: %s", response.Body.String())
	}
}

func TestReadyzOnlyExactReadyStatusReturnsOK(t *testing.T) {
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
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.GET("/readyz", ReadyzWithProvider(healthReadinessStub{snapshot: runtimehealth.APISnapshot{
				Status: test.status, Role: "api", Checks: map[string]string{"database": "ok"},
			}}))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != test.code {
				t.Fatalf("expected %d, got %d", test.code, response.Code)
			}
		})
	}
}
