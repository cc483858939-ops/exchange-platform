package recommendation

import (
	"time"

	"Go.exchange/metrics"
)

type PrometheusMetrics struct{}

func NewPrometheusMetrics() Metrics { return PrometheusMetrics{} }

func (PrometheusMetrics) RecordRequest(outcome, strategyID string) {
	metrics.RecordRecommendationRequest(outcome, strategyID)
}
func (PrometheusMetrics) ObserveCandidateCount(count int) {
	metrics.ObserveRecommendationCandidateCount(count)
}
func (PrometheusMetrics) ObserveResultCount(count int) {
	metrics.ObserveRecommendationResultCount(count)
}
func (PrometheusMetrics) ObserveGenerationDuration(strategyID string, duration time.Duration) {
	metrics.ObserveRecommendationGenerationDuration(strategyID, duration)
}
func (PrometheusMetrics) AddRecallCandidates(source string, count int) {
	metrics.AddRecommendationRecallCandidates(source, count)
}
func (PrometheusMetrics) AddResultsBySource(source string, count int) {
	metrics.AddRecommendationResultsBySource(source, count)
}
func (PrometheusMetrics) AddResultsByClass(class string, count int) {
	metrics.AddRecommendationResultsByClass(class, count)
}
func (PrometheusMetrics) AddResultsBySelection(mode, reason string, count int) {
	metrics.AddRecommendationResultsBySelection(mode, reason, count)
}
func (PrometheusMetrics) AddTrackingResults(status string, count int) {
	metrics.AddRecommendationTrackingResults(status, count)
}
func (PrometheusMetrics) RecordHistoryLoadFailure(viewer ViewerKind) {
	if viewer == ViewerAuthenticated {
		metrics.RecordRecommendationServedHistoryLoadFailure()
	}
}
func (PrometheusMetrics) RecordTracePersistFailure() {
	metrics.RecordRecommendationTracePersistFailure()
}
func (PrometheusMetrics) RecordProfileLoad(status string) {
	metrics.RecordRecommendationProfileLoad(status)
}
func (PrometheusMetrics) ObserveProfileAge(age time.Duration) {
	metrics.ObserveRecommendationProfileAge(age)
}

var _ Metrics = PrometheusMetrics{}
