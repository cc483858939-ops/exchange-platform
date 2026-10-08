package metrics

import (
	"errors"
	"testing"
	"time"
)

func TestOutboxRetentionReportsRowsAndOutcomes(t *testing.T) {
	beforeRows := metricSampleValue(t, outboxRetentionRows)
	full := metricSampleValue(t, outboxRetentionRuns.WithLabelValues("full"))
	idle := metricSampleValue(t, outboxRetentionRuns.WithLabelValues("caught_up"))
	failed := metricSampleValue(t, outboxRetentionRuns.WithLabelValues("error"))
	RecordOutboxRetentionBatch(5000, 5000, 10*time.Millisecond, nil)
	RecordOutboxRetentionBatch(12, 5000, 5*time.Millisecond, nil)
	RecordOutboxRetentionBatch(5000, 5000, time.Millisecond, errors.New("CDC slot unavailable"))
	if got := metricSampleValue(t, outboxRetentionRows) - beforeRows; got != 5012 {
		t.Fatalf("successful deleted rows=%v, want 5012", got)
	}
	if metricSampleValue(t, outboxRetentionRuns.WithLabelValues("full")) != full+1 ||
		metricSampleValue(t, outboxRetentionRuns.WithLabelValues("caught_up")) != idle+1 ||
		metricSampleValue(t, outboxRetentionRuns.WithLabelValues("error")) != failed+1 {
		t.Fatal("full, caught-up and failed batches must have independent outcomes")
	}
}
