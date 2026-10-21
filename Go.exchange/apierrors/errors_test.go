package apierrors

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteInternalErrorUsesSafeContractAndRequestID(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	engine := gin.New()
	engine.Use(RequestIDMiddleware())
	engine.GET("/api/private", func(ctx *gin.Context) {
		WriteInternalError(ctx, errors.New("postgres://admin:TEST_A2_PASSWORD@db.internal/app SELECT * FROM private_users TEST_A2_SQL"))
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	request.Header.Set(RequestIDHeader, "client-controlled-id")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusInternalServerError)
	}
	id := response.Header().Get(RequestIDHeader)
	if id == "" || id == "client-controlled-id" {
		t.Fatalf("server request ID=%q, want a generated ID", id)
	}
	for _, sensitive := range []string{"TEST_A2_PASSWORD", "db.internal", "TEST_A2_SQL", "private_users", "postgres://"} {
		if strings.Contains(response.Body.String(), sensitive) {
			t.Fatalf("response body exposed %q: headers=%v body=%s", sensitive, response.Header(), response.Body.String())
		}
		for name, values := range response.Header() {
			if strings.Contains(name, sensitive) || strings.Contains(strings.Join(values, ","), sensitive) {
				t.Fatalf("response header exposed %q: headers=%v", sensitive, response.Header())
			}
		}
		if strings.Contains(logs.String(), sensitive) {
			t.Fatalf("diagnostic log exposed %q: %s", sensitive, logs.String())
		}
	}
	if !strings.Contains(response.Body.String(), `"code":"INTERNAL_ERROR"`) ||
		!strings.Contains(response.Body.String(), `"error":"Internal server error"`) ||
		!strings.Contains(response.Body.String(), `"request_id":"`+id+`"`) {
		t.Fatalf("unexpected response contract: %s", response.Body.String())
	}
	for _, field := range []string{"request_id", "method", "route", "status_code", "public_error_code", "internal_error_category", "service_operation"} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("diagnostic log missing %q: %s", field, logs.String())
		}
	}
}

func TestWritePublicErrorUnknownCodeFallsBackToInternal(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	engine := gin.New()
	engine.GET("/api/unknown", func(ctx *gin.Context) { WritePublicError(ctx, Code("UNREVIEWED_CODE")) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"INTERNAL_ERROR"`) {
		t.Fatalf("unknown public code was not safely rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHandleRequestErrorKeepsTimeoutAndCancellationSemantics(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	engine := gin.New()
	engine.Use(RequestIDMiddleware())
	engine.GET("/api/timeout", func(ctx *gin.Context) {
		if !HandleRequestError(ctx, context.DeadlineExceeded) {
			t.Error("deadline was not classified")
		}
	})
	engine.GET("/api/cancelled", func(ctx *gin.Context) {
		if !HandleRequestError(ctx, ctx.Request.Context().Err()) {
			t.Error("cancellation was not classified")
		}
	})

	timeout := httptest.NewRecorder()
	engine.ServeHTTP(timeout, httptest.NewRequest(http.MethodGet, "/api/timeout", nil))
	if timeout.Code != http.StatusGatewayTimeout || !strings.Contains(timeout.Body.String(), `"code":"REQUEST_TIMEOUT"`) {
		t.Fatalf("timeout response status=%d body=%s", timeout.Code, timeout.Body.String())
	}

	cancelled := httptest.NewRecorder()
	cancelContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/api/cancelled", nil).WithContext(cancelContext)
	engine.ServeHTTP(cancelled, request)
	if cancelled.Body.Len() != 0 {
		t.Fatalf("cancelled request wrote a response body: %s", cancelled.Body.String())
	}
}

func TestInternalErrorDoesNotRewriteCommittedResponse(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	engine := gin.New()
	engine.GET("/api/partial", func(ctx *gin.Context) {
		ctx.String(http.StatusAccepted, "partial")
		WriteInternalError(ctx, errors.New("TEST_A2_PRIVATE_ERROR"))
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/partial", nil))
	if response.Code != http.StatusAccepted || response.Body.String() != "partial" {
		t.Fatalf("committed response changed: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRecoveryReturnsSafeJSONOnlyForAPIJSONRoutes(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	engine := gin.New()
	engine.Use(Recovery(), RequestIDMiddleware())
	engine.GET("/api/private", func(*gin.Context) { panic("TEST_A2_PANIC_SECRET") })
	engine.GET("/healthz", func(*gin.Context) { panic("TEST_A2_PANIC_SECRET") })

	api := httptest.NewRecorder()
	engine.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/private", nil))
	if api.Code != http.StatusInternalServerError || strings.Contains(api.Body.String(), "TEST_A2_PANIC_SECRET") || !strings.Contains(api.Body.String(), `"code":"INTERNAL_ERROR"`) {
		t.Fatalf("API panic response is not safe: status=%d body=%s", api.Code, api.Body.String())
	}
	if !strings.Contains(logs.String(), "request_id="+api.Header().Get(RequestIDHeader)) || strings.Contains(logs.String(), "TEST_A2_PANIC_SECRET") {
		t.Fatalf("panic diagnostic is not correlated safely: headers=%v logs=%s", api.Header(), logs.String())
	}

	health := httptest.NewRecorder()
	engine.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusInternalServerError || strings.Contains(health.Body.String(), "TEST_A2_PANIC_SECRET") {
		t.Fatalf("health panic response leaked panic: status=%d body=%s", health.Code, health.Body.String())
	}
}
