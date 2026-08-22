package metrics

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMiddlewareRecordsHTTPMetricsAndSkipsMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/metrics", gin.WrapH(Handler()))
	r := httptest.NewRecorder()
	router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if r.Code != http.StatusNoContent {
		t.Fatal(r.Code)
	}
	r = httptest.NewRecorder()
	router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := r.Body.String()
	if !strings.Contains(body, `go_exchange_http_requests_total{method="GET",route="/ping",status="204"} `) {
		t.Fatal(body)
	}
	if strings.Contains(body, `route="/metrics"`) {
		t.Fatal(body)
	}
}
func TestHandlerExposesPipelineMetrics(t *testing.T) {
	SetOutboxRowsTotal(11)
	SetOutboxOldestRowAgeSeconds(7)
	SetConsumerInboxRows("goexchange-notification-projection-v1", 13)
	RecordNotificationProjectionFailure("database")
	RecordNotificationProjectionDLQ()
	ObserveNotificationProjectionLatency(time.Second)
	RecordRecommendationTelemetryEvent("accepted", "impression", "")
	RecordRecommendationTelemetryProjection("applied")
	r := httptest.NewRecorder()
	Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := r.Body.String()
	if !strings.Contains(body, "go_exchange_outbox_rows_total 11") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "go_exchange_outbox_oldest_row_age_seconds 7") {
		t.Fatal(body)
	}
	if !strings.Contains(body, `go_exchange_consumer_inbox_rows_total{consumer="goexchange-notification-projection-v1"} 13`) {
		t.Fatal(body)
	}
	if !strings.Contains(body, `go_exchange_recommendation_telemetry_events_total{event_type="impression",reason="",status="accepted"} 1`) {
		t.Fatal(body)
	}
	if !strings.Contains(body, `go_exchange_recommendation_telemetry_projection_total{status="applied"} 1`) {
		t.Fatal(body)
	}
}
