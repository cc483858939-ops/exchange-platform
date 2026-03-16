package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareRecordsHTTPMetricsAndSkipsMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(Middleware())
	router.GET("/ping", func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	router.GET("/metrics", gin.WrapH(Handler()))

	pingRequest := httptest.NewRequest(http.MethodGet, "/ping", nil)
	pingRecorder := httptest.NewRecorder()
	router.ServeHTTP(pingRecorder, pingRequest)

	if pingRecorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected ping status: got %d want %d", pingRecorder.Code, http.StatusNoContent)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRecorder := httptest.NewRecorder()
	router.ServeHTTP(metricsRecorder, metricsRequest)

	if metricsRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status: got %d want %d", metricsRecorder.Code, http.StatusOK)
	}

	body := metricsRecorder.Body.String()
	if !strings.Contains(body, `go_exchange_http_requests_total{method="GET",route="/ping",status="204"} `) {
		t.Fatalf("expected ping counter in metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, `go_exchange_http_request_duration_seconds_count{method="GET",route="/ping",status="204"} `) {
		t.Fatalf("expected ping duration histogram in metrics output, got:\n%s", body)
	}
	if strings.Contains(body, `route="/metrics"`) {
		t.Fatalf("expected /metrics endpoint to be skipped by middleware, got:\n%s", body)
	}
}

func TestHandlerExposesBusinessMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	SetDynamicWorkersFunc(func() float64 { return 7 })
	SetDirtyBacklog(11)
	t.Cleanup(func() {
		SetDynamicWorkersFunc(nil)
		SetDirtyBacklog(0)
	})

	router := gin.New()
	router.GET("/metrics", gin.WrapH(Handler()))

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status: got %d want %d", recorder.Code, http.StatusOK)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "go_exchange_tasks_dynamic_workers 7") {
		t.Fatalf("expected dynamic worker gauge in metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, "go_exchange_tasks_dirty_backlog 11") {
		t.Fatalf("expected dirty backlog gauge in metrics output, got:\n%s", body)
	}
}
