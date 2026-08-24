package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetupRouterIgnoresForwardedHeadersWithoutTrustedProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "203.0.113.10:1234", "1.2.3.4", "")
	if got := strings.TrimSpace(response.Body.String()); got != "203.0.113.10" {
		t.Fatalf("ClientIP=%q, want direct source", got)
	}
}

func TestSetupRouterUsesForwardedClientIPFromTrustedProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "203.0.113.10/32")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "203.0.113.10:1234", "1.2.3.4", "")
	if got := strings.TrimSpace(response.Body.String()); got != "1.2.3.4" {
		t.Fatalf("ClientIP=%q, want forwarded source", got)
	}
}

func TestSetupRouterIgnoresForwardedHeadersFromUntrustedSource(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "203.0.113.10/32")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "198.51.100.10:1234", "1.2.3.4", "5.6.7.8")
	if got := strings.TrimSpace(response.Body.String()); got != "198.51.100.10" {
		t.Fatalf("ClientIP=%q, want direct source", got)
	}
}

func TestSetupRouterRejectsInvalidTrustedProxyConfiguration(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "0.0.0.0/0")
	if _, err := SetupRouter(nil, nil, nil, nil); err == nil {
		t.Fatal("SetupRouter unexpectedly accepted a trust-all proxy configuration")
	}
}

func newClientIPTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine, err := SetupRouter(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/__client-ip", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, ctx.ClientIP())
	})
	return engine
}

func serveClientIPRequest(engine *gin.Engine, remoteAddr, forwardedFor, realIP string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/__client-ip", nil)
	request.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if realIP != "" {
		request.Header.Set("X-Real-IP", realIP)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
