package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	kafkaCommittedLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_kafka_consumer_group_committed_lag", Help: "Sum of log end minus committed offsets over all retained topic partitions; includes prefetched uncommitted messages. Use sample validity and freshness."}, []string{"group", "topic"})
	kafkaLagValid     = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_kafka_consumer_group_lag_sample_valid", Help: "Whether the latest committed lag sample succeeded; also check last success age."}, []string{"group", "topic"})
	kafkaLagSuccess   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_kafka_consumer_group_lag_last_success_timestamp_seconds", Help: "Last successful committed lag sample time."}, []string{"group", "topic"})
	kafkaLagFailures  = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_kafka_consumer_group_lag_sample_failures_total", Help: "Failed committed lag samples."}, []string{"group", "topic"})
	outboxAgeValid    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_oldest_row_age_sample_valid", Help: "Whether the latest oldest row age sample succeeded; also check last success age."})
	outboxAgeSuccess  = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_oldest_row_age_last_success_timestamp_seconds", Help: "Last successful oldest row age sample time, including an empty table."})
	outboxAgeFailures = prometheus.NewCounter(prometheus.CounterOpts{Name: "go_exchange_outbox_oldest_row_age_sample_failures_total", Help: "Failed oldest outbox row age samples."})
)

func init() {
	registry.MustRegister(kafkaCommittedLag, kafkaLagValid, kafkaLagSuccess, kafkaLagFailures, outboxAgeValid, outboxAgeSuccess, outboxAgeFailures)
}

func RecordKafkaCommittedLagSample(group, topic string, lag int64, at time.Time, valid bool) {
	if !valid {
		kafkaLagValid.WithLabelValues(group, topic).Set(0)
		kafkaLagFailures.WithLabelValues(group, topic).Inc()
		return
	}
	kafkaCommittedLag.WithLabelValues(group, topic).Set(float64(lag))
	kafkaLagSuccess.WithLabelValues(group, topic).Set(float64(at.Unix()))
	kafkaLagValid.WithLabelValues(group, topic).Set(1)
}

func RecordOutboxOldestAgeSample(age float64, at time.Time, valid bool) {
	if !valid {
		outboxAgeValid.Set(0)
		outboxAgeFailures.Inc()
		return
	}
	SetOutboxOldestRowAgeSeconds(age)
	outboxAgeSuccess.Set(float64(at.Unix()))
	outboxAgeValid.Set(1)
}
