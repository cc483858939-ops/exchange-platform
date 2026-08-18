package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry                              = prometheus.NewRegistry()
	httpRequestsTotal                     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_http_requests_total", Help: "Total number of HTTP requests handled by the Gin server."}, []string{"method", "route", "status"})
	httpRequestDuration                   = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "go_exchange_http_request_duration_seconds", Help: "HTTP request latency in seconds.", Buckets: prometheus.DefBuckets}, []string{"method", "route", "status"})
	articleEmbeddingJobs                  = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_article_embedding_jobs", Help: "Current article embedding jobs by state."}, []string{"state"})
	outboxPending                         = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_pending_events", Help: "Current unpublished outbox event count."})
	likePipelineDepth                     = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_like_pipeline_depth", Help: "Current Redis like pipeline depth by stage."}, []string{"stage"})
	recommendationTelemetryEvents         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_telemetry_events_total", Help: "Recommendation telemetry events by ingestion outcome."}, []string{"status", "event_type", "reason"})
	recommendationTelemetryBatchSize      = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_telemetry_batch_size", Help: "Number of recommendation telemetry events per ingestion request.", Buckets: []float64{1, 5, 10, 20, 50}})
	recommendationTelemetryIngestDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_telemetry_ingest_duration_seconds", Help: "Recommendation telemetry ingestion latency in seconds.", Buckets: prometheus.DefBuckets})
	recommendationTelemetryProjection     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_telemetry_projection_total", Help: "Recommendation telemetry projection outcomes."}, []string{"status"})
	recommendationRequests                = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_requests_total", Help: "Recommendation requests by outcome and strategy."}, []string{"outcome", "strategy_id"})
	recommendationRequestLogFailures      = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_request_log_failures_total", Help: "Recommendation request records that failed to persist."})
	recommendationTrackingResults         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_tracking_results_total", Help: "Returned recommendation results by tracking availability."}, []string{"status"})
	recommendationCandidateCount          = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_candidate_count", Help: "Candidate count for completed recommendation requests.", Buckets: []float64{0, 1, 5, 10, 20, 50, 100, 200}})
	recommendationResultCount             = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_result_count", Help: "Result count for completed recommendation requests.", Buckets: []float64{0, 1, 5, 10, 20, 50}})
	recommendationGenerationDuration      = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "go_exchange_recommendation_generation_duration_seconds", Help: "Recommendation generation duration.", Buckets: prometheus.DefBuckets}, []string{"strategy_id"})
	recommendationRecallCandidates        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_recall_candidates_total", Help: "Distinct recommendation candidates by source."}, []string{"source"})
	recommendationResultsBySource         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_results_by_source_total", Help: "Returned recommendation results by source."}, []string{"source"})
	recommendationResultsByClass          = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_results_by_class_total", Help: "Returned recommendation results by class."}, []string{"class"})
	recommendationServedHistoryFailures   = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_served_history_load_failures_total", Help: "Recommendation served-history load failures."})
	recommendationTracePersistFailures    = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_persist_failures_total", Help: "Recommendation serving trace persistence failures."})
	recommendationTraceCleanupFailures    = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_cleanup_failures_total", Help: "Recommendation serving trace cleanup failures."})
	recommendationTraceCleanupRows        = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_cleanup_rows_total", Help: "Recommendation serving trace rows cleaned up."})
)

func init() {
	registry.MustRegister(
		httpRequestsTotal, httpRequestDuration, articleEmbeddingJobs, outboxPending, likePipelineDepth,
		recommendationTelemetryEvents, recommendationTelemetryBatchSize,
		recommendationTelemetryIngestDuration, recommendationTelemetryProjection, recommendationRequests,
		recommendationRequestLogFailures, recommendationTrackingResults,
		recommendationCandidateCount, recommendationResultCount, recommendationGenerationDuration, recommendationRecallCandidates, recommendationResultsBySource, recommendationResultsByClass, recommendationServedHistoryFailures, recommendationTracePersistFailures, recommendationTraceCleanupFailures, recommendationTraceCleanupRows,
	)
}

func Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.Path == "/metrics" {
			ctx.Next()
			return
		}
		started := time.Now()
		ctx.Next()
		route := ctx.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(ctx.Writer.Status())
		httpRequestsTotal.WithLabelValues(ctx.Request.Method, route, status).Inc()
		httpRequestDuration.WithLabelValues(ctx.Request.Method, route, status).Observe(time.Since(started).Seconds())
	}
}

func Handler() http.Handler { return promhttp.HandlerFor(registry, promhttp.HandlerOpts{}) }
func SetArticleEmbeddingJobs(state string, value float64) {
	articleEmbeddingJobs.WithLabelValues(state).Set(value)
}
func SetOutboxPending(value float64) { outboxPending.Set(value) }
func SetLikePipelineDepth(stage string, value float64) {
	likePipelineDepth.WithLabelValues(stage).Set(value)
}
func RecordRecommendationTelemetryEvent(status, eventType, reason string) {
	recommendationTelemetryEvents.WithLabelValues(status, eventType, reason).Inc()
}
func ObserveRecommendationTelemetryBatchSize(size int) {
	recommendationTelemetryBatchSize.Observe(float64(size))
}
func ObserveRecommendationTelemetryIngestDuration(duration time.Duration) {
	recommendationTelemetryIngestDuration.Observe(duration.Seconds())
}
func RecordRecommendationTelemetryProjection(status string) {
	recommendationTelemetryProjection.WithLabelValues(status).Inc()
}
func RecordRecommendationRequest(outcome, strategyID string) {
	recommendationRequests.WithLabelValues(outcome, strategyID).Inc()
}
func RecordRecommendationRequestLogFailure() { recommendationRequestLogFailures.Inc() }
func AddRecommendationTrackingResults(status string, count int) {
	if count > 0 {
		recommendationTrackingResults.WithLabelValues(status).Add(float64(count))
	}
}
func ObserveRecommendationCandidateCount(count int) {
	recommendationCandidateCount.Observe(float64(count))
}
func ObserveRecommendationResultCount(count int) { recommendationResultCount.Observe(float64(count)) }
func ObserveRecommendationGenerationDuration(strategyID string, duration time.Duration) {
	recommendationGenerationDuration.WithLabelValues(strategyID).Observe(duration.Seconds())
}

func AddRecommendationRecallCandidates(source string, count int) {
	if count > 0 {
		recommendationRecallCandidates.WithLabelValues(source).Add(float64(count))
	}
}
func AddRecommendationResultsBySource(source string, count int) {
	if count > 0 {
		recommendationResultsBySource.WithLabelValues(source).Add(float64(count))
	}
}
func AddRecommendationResultsByClass(class string, count int) {
	if count > 0 {
		recommendationResultsByClass.WithLabelValues(class).Add(float64(count))
	}
}
func RecordRecommendationServedHistoryLoadFailure() { recommendationServedHistoryFailures.Inc() }
func RecordRecommendationTracePersistFailure()      { recommendationTracePersistFailures.Inc() }
func RecordRecommendationTraceCleanupFailure()      { recommendationTraceCleanupFailures.Inc() }
func AddRecommendationTraceCleanupRows(count int) {
	if count > 0 {
		recommendationTraceCleanupRows.Add(float64(count))
	}
}
