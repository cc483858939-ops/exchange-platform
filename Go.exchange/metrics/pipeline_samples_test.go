package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func metricSampleValue(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	var sample dto.Metric
	if err := metric.Write(&sample); err != nil {
		t.Fatal(err)
	}
	if sample.Gauge != nil {
		return sample.GetGauge().GetValue()
	}
	return sample.GetCounter().GetValue()
}

func TestCommittedLagFailureRetainsIndependentGroupsAndFreshness(t *testing.T) {
	first := "test-sample-first"
	second := "test-sample-second"
	topic := "test-sample-topic"
	stamp := time.Unix(1000, 0)
	RecordKafkaCommittedLagSample(first, topic, 250, stamp, true)
	RecordKafkaCommittedLagSample(second, topic, 0, stamp, true)
	RecordKafkaCommittedLagSample(first, topic, 0, stamp.Add(time.Minute), false)
	if metricSampleValue(t, kafkaCommittedLag.WithLabelValues(first, topic)) != 250 || metricSampleValue(t, kafkaLagSuccess.WithLabelValues(first, topic)) != 1000 || metricSampleValue(t, kafkaLagValid.WithLabelValues(first, topic)) != 0 || metricSampleValue(t, kafkaLagFailures.WithLabelValues(first, topic)) != 1 {
		t.Fatal("failed group sample was cleared or advanced")
	}
	if metricSampleValue(t, kafkaCommittedLag.WithLabelValues(second, topic)) != 0 || metricSampleValue(t, kafkaLagValid.WithLabelValues(second, topic)) != 1 {
		t.Fatal("another group's successful empty sample changed")
	}
	RecordKafkaCommittedLagSample(first, topic, 0, stamp.Add(2*time.Minute), true)
	if metricSampleValue(t, kafkaCommittedLag.WithLabelValues(first, topic)) != 0 || metricSampleValue(t, kafkaLagValid.WithLabelValues(first, topic)) != 1 {
		t.Fatal("successful recovery was not published")
	}
}
