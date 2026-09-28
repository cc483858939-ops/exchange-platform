package dlq

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func TestNormalizeScanLimits(t *testing.T) {
	if limit, scanLimit, err := normalizeScanLimits(0, 0); err != nil || limit != DefaultLimit || scanLimit != DefaultScanLimit {
		t.Fatalf("default limits=%d/%d err=%v", limit, scanLimit, err)
	}
	for _, test := range []struct {
		limit     int
		scanLimit int
	}{
		{limit: -1, scanLimit: 1}, {limit: MaxLimit + 1, scanLimit: 1},
		{limit: 1, scanLimit: -1}, {limit: 1, scanLimit: MaxScanLimit + 1},
	} {
		if _, _, err := normalizeScanLimits(test.limit, test.scanLimit); err == nil {
			t.Fatalf("accepted out-of-range limits %d/%d", test.limit, test.scanLimit)
		}
	}
	if _, _, err := normalizeScanLimits(MaxLimit, MaxScanLimit); err != nil {
		t.Fatal(err)
	}
}

func TestScanLimitReportsPossibleRemainingRecords(t *testing.T) {
	tests := []struct {
		name    string
		scanned int
		limit   int
		ranges  []scanRange
		want    bool
	}{
		{name: "below limit", scanned: 9, limit: 10, ranges: []scanRange{{next: 10, last: 100}}, want: false},
		{name: "more in current partition", scanned: 10, limit: 10, ranges: []scanRange{{next: 10, last: 11}}, want: true},
		{name: "more in later partition", scanned: 10, limit: 10, ranges: []scanRange{{next: 20, last: 20}, {next: 1, last: 2}}, want: true},
		{name: "exactly complete", scanned: 10, limit: 10, ranges: []scanRange{{next: 20, last: 20}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := scanLimitHasRemaining(test.scanned, test.limit, test.ranges); got != test.want {
				t.Fatalf("truncated=%t want=%t", got, test.want)
			}
		})
	}
}

func TestFilterMatchesFailureMetadataAndInclusiveTimeBounds(t *testing.T) {
	record := replayTestRecord("user_behavior_projection", "behavior")
	after := record.Failure.FailedAt
	before := record.Failure.FailedAt
	filter := Filter{Consumer: record.Consumer, SourceTopic: record.Source.Topic, ErrorClass: record.Failure.Class,
		ErrorCode: record.Failure.Code, FailedAfter: &after, FailedBefore: &before}
	if !filter.matches(record) {
		t.Fatal("record on inclusive timestamp bounds did not match")
	}
	filter.Consumer = "wrong"
	if filter.matches(record) {
		t.Fatal("record matched wrong consumer")
	}
}

func TestKafkaDLQRoundTripIntegration(t *testing.T) {
	applicationConfig, publisher := kafkaIntegrationSetup(t)
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	eventID := uuid.NewString()
	failedAt := time.Now().UTC()
	source := kafka.Message{
		Topic: applicationConfig.Kafka.UserBehaviorTopic, Partition: 3, Offset: 982,
		Key: []byte{0, 0xff, 0x81}, Value: []byte{0x80, 0x01, 0xff},
		Headers: []kafka.Header{{Key: "binary", Value: []byte{0xff, 0, 0x80}}, {Key: "empty", Value: []byte{}}}, Time: failedAt.Add(-time.Minute),
	}
	record, err := eventing.NewDeadLetterRecord("user_behavior_projection", eventID, source, eventing.DeadLetterFailure{
		Class: "permanent", Code: "decode_envelope", Reason: "round-trip test", Attempts: 1, FailedAt: failedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := eventing.BuildDeadLetterMessage(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishRaw(ctx, applicationConfig.Kafka.ConsumerDLQTopic, message); err != nil {
		t.Fatal(err)
	}
	reader, err := NewKafkaDLQReader(applicationConfig.Kafka)
	if err != nil {
		t.Fatal(err)
	}
	after := failedAt.Add(-time.Minute)
	locatedRecords, scan, err := reader.Scan(ctx, Filter{Consumer: record.Consumer, EventID: eventID, FailedAfter: &after, Limit: 10, ScanLimit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if len(locatedRecords) != 1 || scan.Matched != 1 || scan.Scanned < 1 {
		t.Fatalf("located=%d scan=%+v", len(locatedRecords), scan)
	}
	located := locatedRecords[0]
	readAt, err := reader.ReadAt(ctx, located.DLQPartition, located.DLQOffset)
	if err != nil {
		t.Fatal(err)
	}
	if readAt.DLQTopic != applicationConfig.Kafka.ConsumerDLQTopic || readAt.Record.Consumer != record.Consumer ||
		readAt.Record.Source.Topic != source.Topic || readAt.Record.Source.Partition != source.Partition || readAt.Record.Source.Offset != source.Offset ||
		!readAt.Record.Source.Time.Equal(source.Time) || readAt.Record.Failure.Code != record.Failure.Code {
		t.Fatalf("DLQ round-trip metadata=%+v", readAt)
	}
	if !bytesEqual(readAt.Record.Source.Key, source.Key) || !bytesEqual(readAt.Record.Source.Value, source.Value) ||
		len(readAt.Record.Source.Headers) != len(source.Headers) || !bytesEqual(readAt.Record.Source.Headers[0].Value, source.Headers[0].Value) ||
		readAt.Record.Source.Headers[1].Value == nil {
		t.Fatalf("DLQ round-trip bytes=%#v", readAt.Record.Source)
	}
}

func TestKafkaReplayIntegration(t *testing.T) {
	applicationConfig, publisher := kafkaIntegrationSetup(t)
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	eventID := uuid.NewString()
	failedAt := time.Now().UTC()
	source := kafka.Message{
		Topic: applicationConfig.Kafka.UserBehaviorTopic, Partition: 7, Offset: 532,
		Key: []byte("replay-key-" + eventID), Value: []byte("opaque-business-payload-" + eventID),
		Headers: []kafka.Header{{Key: "trace", Value: []byte("before-replay")}}, Time: failedAt.Add(-time.Second),
	}
	record, err := eventing.NewDeadLetterRecord("user_behavior_projection", eventID, source, eventing.DeadLetterFailure{
		Class: "permanent", Code: "decode_envelope", Reason: "replay integration", Attempts: 1, FailedAt: failedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	dlqMessage, err := eventing.BuildDeadLetterMessage(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishRaw(ctx, applicationConfig.Kafka.ConsumerDLQTopic, dlqMessage); err != nil {
		t.Fatal(err)
	}
	dlqReader, err := NewKafkaDLQReader(applicationConfig.Kafka)
	if err != nil {
		t.Fatal(err)
	}
	after := failedAt.Add(-time.Minute)
	located, _, err := dlqReader.Scan(ctx, Filter{Consumer: record.Consumer, EventID: eventID, FailedAfter: &after, Limit: 5, ScanLimit: 10000})
	if err != nil || len(located) != 1 {
		t.Fatalf("DLQ lookup records=%d err=%v", len(located), err)
	}
	registry, err := NewReplayPolicyRegistry(applicationConfig.Kafka)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildReplayPlan(located[0], registry)
	if err != nil {
		t.Fatal(err)
	}
	startOffsets := kafkaTopicEndOffsets(t, ctx, applicationConfig.Kafka.Brokers, plan.SourceTopic)
	replayID := uuid.NewString()
	audit := &fakeAuditRepository{}
	result, err := ExecuteReplay(ctx, located[0], plan, replayID, time.Now().UTC(), audit, publisher)
	if err != nil || result.Status != ReplayStatusSucceeded {
		t.Fatalf("execute replay result=%+v err=%v", result, err)
	}
	replayed := readKafkaReplay(t, ctx, applicationConfig.Kafka.Brokers, plan.SourceTopic, startOffsets, replayID)
	if !bytesEqual(replayed.Key, source.Key) || !bytesEqual(replayed.Value, source.Value) || !replayed.Time.After(source.Time) {
		t.Fatalf("replayed Kafka message changed: key=%q value=%q time=%s", replayed.Key, replayed.Value, replayed.Time)
	}
	if string(headerValue(replayed.Headers, "trace")) != "before-replay" || string(headerValue(replayed.Headers, ReplayIDHeader)) != replayID ||
		string(headerValue(replayed.Headers, ReplayHeader)) != "true" {
		t.Fatalf("replayed Kafka headers=%#v", replayed.Headers)
	}
	if replayed.Partition == source.Partition {
		t.Log("replay partition matched the source partition by hash; partition was not forced")
	}
}

func kafkaIntegrationSetup(t *testing.T) (*config.Config, *eventing.KafkaPublisher) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("KAFKA_BROKERS")) == "" {
		t.Skip("set KAFKA_BROKERS to run Kafka DLQ integration test")
	}
	applicationConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := eventing.NewKafkaPublisher(applicationConfig.Kafka)
	if err != nil {
		t.Fatal(err)
	}
	return applicationConfig, publisher
}

func kafkaTopicEndOffsets(t *testing.T, ctx context.Context, brokers []string, topic string) map[int]int64 {
	t.Helper()
	var conn *kafka.Conn
	var err error
	for _, broker := range brokers {
		conn, err = kafka.DialContext(ctx, "tcp", broker)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	partitions, err := conn.ReadPartitions(topic)
	_ = conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	ends := make(map[int]int64, len(partitions))
	for _, partition := range partitions {
		var leader *kafka.Conn
		for _, broker := range brokers {
			leader, err = kafka.DialLeader(ctx, "tcp", broker, topic, partition.ID)
			if err == nil {
				break
			}
		}
		if err != nil {
			t.Fatalf("dial topic=%s partition=%d: %v", topic, partition.ID, err)
		}
		_, last, readErr := leader.ReadOffsets()
		_ = leader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		ends[partition.ID] = last
	}
	return ends
}

func readKafkaReplay(t *testing.T, ctx context.Context, brokers []string, topic string, starts map[int]int64, replayID string) kafka.Message {
	t.Helper()
	ends := kafkaTopicEndOffsets(t, ctx, brokers, topic)
	for partition, end := range ends {
		start := starts[partition]
		if start >= end {
			continue
		}
		reader := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, Partition: partition, StartOffset: start, MinBytes: 1, MaxBytes: 10e6})
		if err := reader.SetOffset(start); err != nil {
			_ = reader.Close()
			t.Fatal(err)
		}
		for {
			message, err := reader.FetchMessage(ctx)
			if err != nil {
				_ = reader.Close()
				t.Fatalf("read replayed message: %v", err)
			}
			if message.Offset >= end {
				break
			}
			if string(headerValue(message.Headers, ReplayIDHeader)) == replayID {
				_ = reader.Close()
				return message
			}
		}
		_ = reader.Close()
	}
	t.Fatal("replayed Kafka record was not found on the source topic")
	return kafka.Message{}
}

func headerValue(headers []kafka.Header, key string) []byte {
	for _, header := range headers {
		if header.Key == key {
			return header.Value
		}
	}
	return nil
}

func bytesEqual(left, right []byte) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestKafkaDLQReaderRequiresExplicitBrokerAndTopic(t *testing.T) {
	if _, err := NewKafkaDLQReader(config.KafkaConfig{ConsumerDLQTopic: "dlq"}); err == nil {
		t.Fatal("reader accepted no brokers")
	}
	if _, err := NewKafkaDLQReader(config.KafkaConfig{Brokers: []string{"localhost:9092"}}); err == nil {
		t.Fatal("reader accepted an empty DLQ topic")
	}
}
