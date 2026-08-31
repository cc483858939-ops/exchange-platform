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
	registry                                     = prometheus.NewRegistry()
	httpRequestsTotal                            = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_http_requests_total", Help: "Total number of HTTP requests handled by the Gin server."}, []string{"method", "route", "status"})
	httpRequestDuration                          = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "go_exchange_http_request_duration_seconds", Help: "HTTP request latency in seconds.", Buckets: prometheus.DefBuckets}, []string{"method", "route", "status"})
	postEmbeddingEvents                          = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_post_embedding_events_total", Help: "Post embedding event outcomes."}, []string{"result"})
	postEmbeddingFailures                        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_post_embedding_failures_total", Help: "Post embedding processing failures by stage."}, []string{"stage"})
	postEmbeddingPublishFailures                 = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_post_embedding_publish_failures_total", Help: "Post embedding publish failures by source."}, []string{"source"})
	postEmbeddingProcessingDuration              = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_post_embedding_processing_duration_seconds", Help: "Post embedding message processing duration in seconds.", Buckets: prometheus.DefBuckets})
	outboxCDCSlotActive                          = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_cdc_slot_active", Help: "Whether the configured PostgreSQL CDC slot is active."})
	outboxCDCWALLagBytes                         = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_cdc_wal_lag_bytes", Help: "WAL lag behind the outbox CDC slot in bytes."})
	outboxCDCSlotConfirmedLSN                    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_cdc_slot_confirmed_lsn", Help: "Confirmed flush LSN reported by the outbox CDC slot."})
	outboxRowsTotal                              = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_rows_total", Help: "Current retained outbox row count."})
	outboxOldestRowAgeSeconds                    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_oldest_row_age_seconds", Help: "Age of the oldest retained outbox row in seconds."})
	notificationConsumerLag                      = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_notification_consumer_lag", Help: "Notification projection consumer lag."})
	consumerInboxRows                            = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_consumer_inbox_rows_total", Help: "ConsumerInbox rows retained for a consumer."}, []string{"consumer"})
	notificationProjectionFailures               = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_notification_projection_failures_total", Help: "Notification projection failures by stage."}, []string{"stage"})
	notificationProjectionDLQ                    = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_notification_projection_dlq_total", Help: "Malformed notification activity messages sent to the DLQ."})
	notificationProjectionLatency                = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_notification_projection_latency_seconds", Help: "Notification projection batch latency in seconds.", Buckets: prometheus.DefBuckets})
	likePipelineDepth                            = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_like_pipeline_depth", Help: "Current Redis like pipeline depth by stage."}, []string{"stage"})
	recommendationTelemetryEvents                = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_telemetry_events_total", Help: "Recommendation telemetry events by ingestion outcome."}, []string{"status", "event_type", "reason"})
	recommendationTelemetryBatchSize             = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_telemetry_batch_size", Help: "Number of recommendation telemetry events per ingestion request.", Buckets: []float64{1, 5, 10, 20, 50}})
	recommendationTelemetryIngestDuration        = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_telemetry_ingest_duration_seconds", Help: "Recommendation telemetry ingestion latency in seconds.", Buckets: prometheus.DefBuckets})
	recommendationTelemetryProjection            = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_telemetry_projection_total", Help: "Recommendation telemetry projection outcomes."}, []string{"status"})
	recommendationRequests                       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_requests_total", Help: "Recommendation requests by outcome and strategy."}, []string{"outcome", "strategy_id"})
	recommendationRequestLogFailures             = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_request_log_failures_total", Help: "Recommendation request records that failed to persist."})
	recommendationTrackingResults                = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_tracking_results_total", Help: "Returned recommendation results by tracking availability."}, []string{"status"})
	recommendationCandidateCount                 = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_candidate_count", Help: "Candidate count for completed recommendation requests.", Buckets: []float64{0, 1, 5, 10, 20, 50, 100, 200}})
	recommendationResultCount                    = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_result_count", Help: "Result count for completed recommendation requests.", Buckets: []float64{0, 1, 5, 10, 20, 50}})
	recommendationGenerationDuration             = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "go_exchange_recommendation_generation_duration_seconds", Help: "Recommendation generation duration.", Buckets: prometheus.DefBuckets}, []string{"strategy_id"})
	recommendationRecallCandidates               = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_recall_candidates_total", Help: "Distinct recommendation candidates by source."}, []string{"source"})
	recommendationResultsBySource                = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_results_by_source_total", Help: "Returned recommendation results by source."}, []string{"source"})
	recommendationResultsByClass                 = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_results_by_class_total", Help: "Returned recommendation results by class."}, []string{"class"})
	recommendationResultsBySelection             = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_results_by_selection_total", Help: "Returned recommendation results by selection mode and exploration reason."}, []string{"mode", "reason"})
	recommendationServedHistoryFailures          = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_served_history_load_failures_total", Help: "Recommendation served-history load failures."})
	recommendationTracePersistFailures           = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_persist_failures_total", Help: "Recommendation serving trace persistence failures."})
	recommendationTraceCleanupFailures           = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_cleanup_failures_total", Help: "Recommendation serving trace cleanup failures."})
	recommendationTraceCleanupRows               = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_recommendation_trace_cleanup_rows_total", Help: "Recommendation serving trace rows cleaned up."})
	recommendationProfileLoad                    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_profile_load_total", Help: "Materialized recommendation profile load outcomes."}, []string{"status"})
	recommendationProfileAge                     = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_profile_age_seconds", Help: "Age in seconds of materialized profiles used for serving.", Buckets: prometheus.DefBuckets})
	recommendationProfileMaterialization         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_recommendation_profile_materialization_total", Help: "Recommendation profile materialization outcomes."}, []string{"result"})
	recommendationProfileMaterializationDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_recommendation_profile_materialization_duration_seconds", Help: "Recommendation profile materialization duration in seconds.", Buckets: prometheus.DefBuckets})
	recommendationProfileDirtyQueueDepth         = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_recommendation_profile_dirty_queue_depth", Help: "Current recommendation profile dirty queue depth."})
	runtimeReadiness                             = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "runtime_readiness", Help: "Runtime readiness by role and check."}, []string{"role", "check"})
	runtimeReadinessTransitions                  = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "runtime_readiness_transitions_total", Help: "Runtime readiness state transitions."}, []string{"role", "from", "to", "reason"})
	runtimeReadinessLastSuccess                  = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "runtime_readiness_last_success_timestamp", Help: "Unix timestamp of the last successful readiness evaluation."}, []string{"role"})
	runtimeReadinessLastEvaluation               = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "runtime_readiness_last_evaluation_timestamp", Help: "Unix timestamp of the most recent readiness evaluation."}, []string{"role"})
	workerPipelineHealthy                        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "worker_pipeline_healthy", Help: "Whether a worker pipeline is healthy."}, []string{"pipeline"})
	workerPipelineConsecutiveFailures            = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "worker_pipeline_consecutive_failures", Help: "Consecutive failures for a worker pipeline."}, []string{"pipeline"})
	workerPipelineLastSuccess                    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "worker_pipeline_last_success_timestamp", Help: "Unix timestamp of the last successful worker pipeline operation."}, []string{"pipeline"})
	workerPipelineBacklog                        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "worker_pipeline_backlog", Help: "Current backlog for a worker pipeline."}, []string{"pipeline"})
	workerPipelineBacklogStalled                 = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "worker_pipeline_backlog_stalled", Help: "Whether a worker pipeline backlog has exceeded its grace period."}, []string{"pipeline"})
)

func init() {
	registry.MustRegister(
		httpRequestsTotal, httpRequestDuration, postEmbeddingEvents, postEmbeddingFailures, postEmbeddingPublishFailures, postEmbeddingProcessingDuration,
		outboxCDCSlotActive, outboxCDCWALLagBytes, outboxCDCSlotConfirmedLSN, outboxRowsTotal, outboxOldestRowAgeSeconds, notificationConsumerLag, consumerInboxRows, notificationProjectionFailures, notificationProjectionDLQ, notificationProjectionLatency, likePipelineDepth,
		recommendationTelemetryEvents, recommendationTelemetryBatchSize,
		recommendationTelemetryIngestDuration, recommendationTelemetryProjection, recommendationRequests,
		recommendationRequestLogFailures, recommendationTrackingResults,
		recommendationCandidateCount, recommendationResultCount, recommendationGenerationDuration, recommendationRecallCandidates, recommendationResultsBySource, recommendationResultsByClass, recommendationResultsBySelection, recommendationServedHistoryFailures, recommendationTracePersistFailures, recommendationTraceCleanupFailures, recommendationTraceCleanupRows,
		recommendationProfileLoad, recommendationProfileAge, recommendationProfileMaterialization, recommendationProfileMaterializationDuration, recommendationProfileDirtyQueueDepth,
		runtimeReadiness, runtimeReadinessTransitions, runtimeReadinessLastSuccess, runtimeReadinessLastEvaluation,
		workerPipelineHealthy, workerPipelineConsecutiveFailures, workerPipelineLastSuccess, workerPipelineBacklog, workerPipelineBacklogStalled,
	)
	for _, result := range []string{"generated", "up_to_date", "post_missing", "post_unavailable", "invalid_event", "provider_non_retryable"} {
		postEmbeddingEvents.WithLabelValues(result)
	}
	for _, stage := range []string{"decode", "db_read", "provider", "db_upsert", "kafka_commit"} {
		postEmbeddingFailures.WithLabelValues(stage)
	}
	for _, source := range []string{"post_create", "requeue"} {
		postEmbeddingPublishFailures.WithLabelValues(source)
	}
	for _, status := range []string{"hit", "stale", "miss", "incompatible", "error"} {
		recommendationProfileLoad.WithLabelValues(status)
	}
	for _, result := range []string{"success", "error", "lock_skipped"} {
		recommendationProfileMaterialization.WithLabelValues(result)
	}
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
func RecordPostEmbeddingEvent(result string) {
	postEmbeddingEvents.WithLabelValues(result).Inc()
}
func RecordPostEmbeddingFailure(stage string) {
	postEmbeddingFailures.WithLabelValues(stage).Inc()
}
func RecordPostEmbeddingPublishFailure(source string) {
	postEmbeddingPublishFailures.WithLabelValues(source).Inc()
}
func ObservePostEmbeddingProcessingDuration(duration time.Duration) {
	postEmbeddingProcessingDuration.Observe(duration.Seconds())
}
func SetOutboxCDCSlotActive(value float64)       { outboxCDCSlotActive.Set(value) }
func SetOutboxCDCWALLagBytes(value float64)      { outboxCDCWALLagBytes.Set(value) }
func SetOutboxCDCSlotConfirmedLSN(value float64) { outboxCDCSlotConfirmedLSN.Set(value) }
func SetOutboxRowsTotal(value float64)           { outboxRowsTotal.Set(value) }
func SetOutboxOldestRowAgeSeconds(value float64) { outboxOldestRowAgeSeconds.Set(value) }
func SetNotificationConsumerLag(value float64)   { notificationConsumerLag.Set(value) }
func SetConsumerInboxRows(consumer string, value float64) {
	if consumer != "" {
		consumerInboxRows.WithLabelValues(consumer).Set(value)
	}
}
func RecordNotificationProjectionFailure(stage string) {
	notificationProjectionFailures.WithLabelValues(stage).Inc()
}
func RecordNotificationProjectionDLQ() { notificationProjectionDLQ.Inc() }
func ObserveNotificationProjectionLatency(duration time.Duration) {
	notificationProjectionLatency.Observe(duration.Seconds())
}
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
func AddRecommendationResultsBySelection(mode, reason string, count int) {
	if count > 0 {
		recommendationResultsBySelection.WithLabelValues(mode, reason).Add(float64(count))
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

func RecordRecommendationProfileLoad(status string) {
	recommendationProfileLoad.WithLabelValues(status).Inc()
}

func ObserveRecommendationProfileAge(age time.Duration) {
	if age >= 0 {
		recommendationProfileAge.Observe(age.Seconds())
	}
}

func RecordRecommendationProfileMaterialization(result string) {
	recommendationProfileMaterialization.WithLabelValues(result).Inc()
}

func ObserveRecommendationProfileMaterializationDuration(duration time.Duration) {
	recommendationProfileMaterializationDuration.Observe(duration.Seconds())
}

func SetRecommendationProfileDirtyQueueDepth(value float64) {
	recommendationProfileDirtyQueueDepth.Set(value)
}

func SetRuntimeReadiness(role, check string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	runtimeReadiness.WithLabelValues(role, check).Set(value)
}

func RecordRuntimeReadinessTransition(role, from, to, reason string) {
	runtimeReadinessTransitions.WithLabelValues(role, from, to, reason).Inc()
}

func SetRuntimeReadinessLastSuccess(role string, timestamp time.Time) {
	if !timestamp.IsZero() {
		runtimeReadinessLastSuccess.WithLabelValues(role).Set(float64(timestamp.Unix()))
	}
}

func SetRuntimeReadinessLastEvaluation(role string, timestamp time.Time) {
	if !timestamp.IsZero() {
		runtimeReadinessLastEvaluation.WithLabelValues(role).Set(float64(timestamp.Unix()))
	}
}

func SetWorkerPipelineHealthy(pipeline string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	workerPipelineHealthy.WithLabelValues(pipeline).Set(value)
}

func SetWorkerPipelineConsecutiveFailures(pipeline string, failures int) {
	workerPipelineConsecutiveFailures.WithLabelValues(pipeline).Set(float64(failures))
}

func SetWorkerPipelineLastSuccess(pipeline string, timestamp time.Time) {
	if !timestamp.IsZero() {
		workerPipelineLastSuccess.WithLabelValues(pipeline).Set(float64(timestamp.Unix()))
	}
}

func SetWorkerPipelineBacklog(pipeline string, backlog int64) {
	if backlog < 0 {
		backlog = 0
	}
	workerPipelineBacklog.WithLabelValues(pipeline).Set(float64(backlog))
}

func SetWorkerPipelineBacklogStalled(pipeline string, stalled bool) {
	value := 0.0
	if stalled {
		value = 1
	}
	workerPipelineBacklogStalled.WithLabelValues(pipeline).Set(value)
}
