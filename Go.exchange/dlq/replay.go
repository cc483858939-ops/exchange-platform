package dlq

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
)

const (
	ReplayHeader             = "x-goexchange-replay"
	ReplayIDHeader           = "x-goexchange-replay-id"
	ReplayedAtHeader         = "x-goexchange-replayed-at"
	ReplayDLQTopicHeader     = "x-goexchange-dlq-topic"
	ReplayDLQPartitionHeader = "x-goexchange-dlq-partition"
	ReplayDLQOffsetHeader    = "x-goexchange-dlq-offset"
)

type ReplayPlan struct {
	DLQTopic        string           `json:"dlq_topic"`
	DLQPartition    int              `json:"dlq_partition"`
	DLQOffset       int64            `json:"dlq_offset"`
	Consumer        string           `json:"consumer"`
	EventID         string           `json:"event_id,omitempty"`
	SourceTopic     string           `json:"source_topic"`
	SourcePartition int              `json:"source_partition"`
	SourceOffset    int64            `json:"source_offset"`
	SafetyMode      ReplaySafetyMode `json:"safety_mode"`
	ErrorClass      string           `json:"error_class"`
	ErrorCode       string           `json:"error_code"`
	FailedAt        time.Time        `json:"failed_at"`
	KeySize         int              `json:"key_size"`
	ValueSize       int              `json:"value_size"`
	HeaderCount     int              `json:"header_count"`
}

func BuildReplayPlan(located LocatedRecord, registry ReplayPolicyRegistry) (ReplayPlan, error) {
	policy, err := registry.Validate(located.Record)
	if err != nil {
		return ReplayPlan{}, err
	}
	if strings.TrimSpace(located.DLQTopic) == "" || located.DLQPartition < 0 || located.DLQOffset < 0 {
		return ReplayPlan{}, errors.New("DLQ location is invalid")
	}
	record := located.Record
	return ReplayPlan{
		DLQTopic: located.DLQTopic, DLQPartition: located.DLQPartition, DLQOffset: located.DLQOffset,
		Consumer: record.Consumer, EventID: record.EventID,
		SourceTopic: record.Source.Topic, SourcePartition: record.Source.Partition, SourceOffset: record.Source.Offset,
		SafetyMode: policy.SafetyMode, ErrorClass: record.Failure.Class, ErrorCode: record.Failure.Code,
		FailedAt: record.Failure.FailedAt, KeySize: len(record.Source.Key), ValueSize: len(record.Source.Value),
		HeaderCount: len(record.Source.Headers),
	}, nil
}

func BuildReplayMessage(located LocatedRecord, replayID string, replayedAt time.Time) (kafka.Message, error) {
	if err := eventing.ValidateDeadLetterRecord(located.Record); err != nil {
		return kafka.Message{}, err
	}
	replayID = strings.TrimSpace(replayID)
	if replayID == "" {
		return kafka.Message{}, errors.New("replay ID is required")
	}
	if replayedAt.IsZero() {
		return kafka.Message{}, errors.New("replay time is required")
	}
	replayedAt = replayedAt.UTC()
	message := kafka.Message{
		Key: cloneReplayBytes(located.Record.Source.Key), Value: cloneReplayBytes(located.Record.Source.Value),
		Headers: replayHeaders(located.Record.Source.Headers, located, replayID, replayedAt), Time: replayedAt,
	}
	return message, nil
}

func replayHeaders(source []eventing.DeadLetterHeader, located LocatedRecord, replayID string, replayedAt time.Time) []kafka.Header {
	headers := make([]kafka.Header, 0, len(source)+6)
	for _, header := range source {
		if isReplayHeader(header.Key) {
			continue
		}
		headers = append(headers, kafka.Header{Key: header.Key, Value: cloneReplayBytes(header.Value)})
	}
	headers = append(headers,
		kafka.Header{Key: ReplayHeader, Value: []byte("true")},
		kafka.Header{Key: ReplayIDHeader, Value: []byte(replayID)},
		kafka.Header{Key: ReplayedAtHeader, Value: []byte(replayedAt.Format(time.RFC3339Nano))},
		kafka.Header{Key: ReplayDLQTopicHeader, Value: []byte(located.DLQTopic)},
		kafka.Header{Key: ReplayDLQPartitionHeader, Value: []byte(fmt.Sprintf("%d", located.DLQPartition))},
		kafka.Header{Key: ReplayDLQOffsetHeader, Value: []byte(fmt.Sprintf("%d", located.DLQOffset))},
	)
	return headers
}

func isReplayHeader(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return lower == ReplayHeader || lower == ReplayIDHeader || lower == ReplayedAtHeader || lower == ReplayDLQTopicHeader || lower == ReplayDLQPartitionHeader || lower == ReplayDLQOffsetHeader
}

func cloneReplayBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
