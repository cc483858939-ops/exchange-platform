package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	postEmbeddingGenerationChanges = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "go_exchange_post_embedding_generation_changes_total",
		Help: "Consumer group generations entered by the Post Embedding consumer.",
	})
	postEmbeddingAssignedPartitions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_exchange_post_embedding_assigned_partitions",
		Help: "Partitions assigned to this Post Embedding consumer group member in the active generation.",
	})
	postEmbeddingHeldPartitions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_exchange_post_embedding_held_partitions",
		Help: "Assigned Post Embedding partitions currently holding an uncommitted batch.",
	})
	postEmbeddingCircuitState = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_exchange_post_embedding_provider_circuit_state",
		Help: "Provider circuit state: 0 closed, 1 open, 2 half-open.",
	})
	postEmbeddingHalfOpenProbes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "go_exchange_post_embedding_provider_half_open_probes_total",
		Help: "Post Embedding provider circuit half-open probe outcomes.",
	}, []string{"result"})
	postEmbeddingQuarantines = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "go_exchange_post_embedding_instance_quarantines_total",
		Help: "Post Embedding instances quarantined after a locally confirmed fault.",
	}, []string{"reason"})
	postEmbeddingCommitFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "go_exchange_post_embedding_partition_commit_failures_total",
		Help: "Post Embedding partition offset commit failures.",
	})
	postEmbeddingCommitUncertain = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "go_exchange_post_embedding_partition_commit_uncertain_total",
		Help: "Post Embedding offset commits whose broker outcome could not be confirmed by the client.",
	})
)

func init() {
	registry.MustRegister(
		postEmbeddingGenerationChanges,
		postEmbeddingAssignedPartitions,
		postEmbeddingHeldPartitions,
		postEmbeddingCircuitState,
		postEmbeddingHalfOpenProbes,
		postEmbeddingQuarantines,
		postEmbeddingCommitFailures,
		postEmbeddingCommitUncertain,
	)
	postEmbeddingCircuitState.Set(0)
}

func RecordPostEmbeddingGenerationChange() { postEmbeddingGenerationChanges.Inc() }
func SetPostEmbeddingAssignedPartitions(partitions int) {
	if partitions < 0 {
		partitions = 0
	}
	postEmbeddingAssignedPartitions.Set(float64(partitions))
}
func SetPostEmbeddingHeldPartitions(partitions int) {
	if partitions < 0 {
		partitions = 0
	}
	postEmbeddingHeldPartitions.Set(float64(partitions))
}
func SetPostEmbeddingCircuitBreakerState(state string) {
	switch strings.TrimSpace(state) {
	case "open":
		postEmbeddingCircuitState.Set(1)
	case "half_open":
		postEmbeddingCircuitState.Set(2)
	default:
		postEmbeddingCircuitState.Set(0)
	}
}
func RecordPostEmbeddingHalfOpenProbe(result string) {
	switch result {
	case "success", "failure", "canceled":
	default:
		result = "failure"
	}
	postEmbeddingHalfOpenProbes.WithLabelValues(result).Inc()
}
func RecordPostEmbeddingInstanceQuarantine(reason string) {
	switch reason {
	case "configuration", "local_network", "local_tls", "operator":
	default:
		reason = "other"
	}
	postEmbeddingQuarantines.WithLabelValues(reason).Inc()
}
func RecordPostEmbeddingPartitionCommitFailure(uncertain bool) {
	postEmbeddingCommitFailures.Inc()
	if uncertain {
		postEmbeddingCommitUncertain.Inc()
	}
}
