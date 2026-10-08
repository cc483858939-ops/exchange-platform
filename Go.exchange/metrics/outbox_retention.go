package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	outboxRetentionRows     = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_outbox_retention_deleted_rows_total", Help: "Outbox rows deleted by retention batches."})
	outboxRetentionRuns     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_outbox_retention_batches_total", Help: "Outbox retention batch outcomes; full batches may have more eligible rows remaining."}, []string{"outcome"})
	outboxRetentionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "go_exchange_outbox_retention_batch_duration_seconds", Help: "Duration of the CDC health check and bounded outbox deletion batch.", Buckets: prometheus.DefBuckets})
)

func init() {
	registry.MustRegister(outboxRetentionRows, outboxRetentionRuns, outboxRetentionDuration)
	for _, outcome := range []string{"full", "caught_up", "error"} {
		outboxRetentionRuns.WithLabelValues(outcome)
	}
}

func RecordOutboxRetentionBatch(rows int64, batchSize int, elapsed time.Duration, err error) {
	outcome := "caught_up"
	if err != nil {
		outcome = "error"
	} else {
		outboxRetentionRows.Add(float64(rows))
		if rows >= int64(batchSize) {
			outcome = "full"
		}
	}
	outboxRetentionRuns.WithLabelValues(outcome).Inc()
	outboxRetentionDuration.Observe(elapsed.Seconds())
}
