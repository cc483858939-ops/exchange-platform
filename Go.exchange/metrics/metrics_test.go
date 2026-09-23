package metrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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
	recommendationTelemetryEvents.WithLabelValues("accepted", "impression", "")
	recommendationTelemetryProjection.WithLabelValues("applied")
	telemetryEventBefore := prometheusMetricValue(t, `go_exchange_recommendation_telemetry_events_total{event_type="impression",reason="",status="accepted"}`)
	telemetryProjectionBefore := prometheusMetricValue(t, `go_exchange_recommendation_telemetry_projection_total{status="applied"}`)
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
	if !strings.Contains(body, fmt.Sprintf(`go_exchange_recommendation_telemetry_events_total{event_type="impression",reason="",status="accepted"} %.0f`, telemetryEventBefore+1)) {
		t.Fatal(body)
	}
	if !strings.Contains(body, fmt.Sprintf(`go_exchange_recommendation_telemetry_projection_total{status="applied"} %.0f`, telemetryProjectionBefore+1)) {
		t.Fatal(body)
	}
}

func TestHandlerExposesKafkaConsumerRecoveryMetrics(t *testing.T) {
	consumers := []string{"like_snapshot_projection", "user_behavior_projection", "recommendation_metrics"}
	for _, consumer := range consumers {
		RecordKafkaConsumerRecovery(consumer, "dlq", "decode_envelope")
	}
	RecordKafkaConsumerRecovery("user_behavior_projection", "unbounded-outcome", "decode_envelope")
	RecordKafkaConsumerRecovery("recommendation_metrics", "dlq", "unbounded-error-code")
	RecordKafkaConsumerRecovery("unbounded-topic", "dlq", "decode_envelope")
	r := httptest.NewRecorder()
	Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := r.Body.String()
	if !strings.Contains(body, `go_exchange_kafka_consumer_recovery_total{code="decode_envelope",consumer="like_snapshot_projection",outcome="dlq"} `) {
		t.Fatal(body)
	}
	for _, consumer := range consumers[1:] {
		if !strings.Contains(body, fmt.Sprintf(`go_exchange_kafka_consumer_recovery_total{code="decode_envelope",consumer="%s",outcome="dlq"} `, consumer)) {
			t.Fatalf("known consumer %q was not exported: %s", consumer, body)
		}
	}
	if strings.Contains(body, "unbounded-error-code") || strings.Contains(body, "unbounded-outcome") || strings.Contains(body, "unbounded-topic") {
		t.Fatal("unbounded Kafka recovery labels were exported")
	}
}

func prometheusMetricValue(t *testing.T, prefix string) float64 {
	t.Helper()
	r := httptest.NewRecorder()
	Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(r.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse metric %q line %q: %v", prefix, line, err)
		}
		return value
	}
	t.Fatalf("metric %q not found", prefix)
	return 0
}
