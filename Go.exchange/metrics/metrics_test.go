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
	consumers := []string{"like_snapshot_projection", "user_behavior_projection", "recommendation_metrics", "post_embedding"}
	outcomes := []string{"message_applied", "batch_applied", "message_noop", "retry_attempt", "retry_exhausted", "message_dlq", "dlq_publish_failed", "redelivery_required"}
	codes := []string{"none", "decode_envelope", "unsupported_event_type", "unsupported_schema", "decode_payload", "invalid_payload", "database_unavailable", "database_transaction", "dlq_publish", "kafka_commit", "provider_retryable", "provider_permanent", "provider_contract_invalid", "source_changed", "internal_state"}
	for _, consumer := range consumers {
		RecordKafkaConsumerRecovery(consumer, "message_dlq", "decode_envelope")
	}
	for _, outcome := range outcomes {
		RecordKafkaConsumerRecovery("post_embedding", outcome, "none")
	}
	for _, code := range codes {
		RecordKafkaConsumerRecovery("post_embedding", "message_noop", code)
	}
	RecordKafkaConsumerRecovery("user_behavior_projection", "unbounded-outcome", "decode_envelope")
	RecordKafkaConsumerRecovery("recommendation_metrics", "message_dlq", "unbounded-error-code")
	RecordKafkaConsumerRecovery("unbounded-topic", "message_dlq", "decode_envelope")
	for _, outcome := range []string{"applied", "noop", "retry", "dlq"} {
		RecordKafkaConsumerRecovery("post_embedding", outcome, "kafka_commit")
	}
	r := httptest.NewRecorder()
	Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := r.Body.String()
	if !strings.Contains(body, "# HELP go_exchange_kafka_consumer_recovery_total Kafka consumer recovery control-flow events by consumer, outcome, and stable code.") {
		t.Fatal("Kafka recovery metric help text is missing or outdated")
	}
	if !strings.Contains(body, `go_exchange_kafka_consumer_recovery_total{code="decode_envelope",consumer="like_snapshot_projection",outcome="message_dlq"} `) {
		t.Fatal(body)
	}
	for _, consumer := range consumers {
		if !strings.Contains(body, fmt.Sprintf(`go_exchange_kafka_consumer_recovery_total{code="decode_envelope",consumer="%s",outcome="message_dlq"} `, consumer)) {
			t.Fatalf("known consumer %q was not exported: %s", consumer, body)
		}
	}
	for _, outcome := range outcomes {
		if !strings.Contains(body, fmt.Sprintf(`consumer="post_embedding",outcome="%s"`, outcome)) {
			t.Fatalf("known outcome %q was not exported", outcome)
		}
	}
	for _, code := range codes {
		if !strings.Contains(body, fmt.Sprintf(`code="%s",consumer="post_embedding",outcome="message_noop"`, code)) {
			t.Fatalf("known code %q was not exported", code)
		}
	}
	for _, outcome := range []string{"applied", "noop", "retry", "dlq"} {
		if strings.Contains(body, fmt.Sprintf(`consumer="post_embedding",outcome="%s"}`, outcome)) {
			t.Fatalf("legacy outcome %q was exported", outcome)
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
