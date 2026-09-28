package eventing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/segmentio/kafka-go"
)

const (
	DeadLetterSchemaVersion  = 1
	MaxDeadLetterReasonBytes = 4096
)

// DeadLetterHeader preserves one source Kafka header without interpreting its bytes.
type DeadLetterHeader struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// DeadLetterSource records the original Kafka location and message bytes.
type DeadLetterSource struct {
	Topic     string             `json:"topic"`
	Partition int                `json:"partition"`
	Offset    int64              `json:"offset"`
	Key       []byte             `json:"key"`
	Value     []byte             `json:"value"`
	Headers   []DeadLetterHeader `json:"headers,omitempty"`
	Time      time.Time          `json:"time,omitempty"`
}

// DeadLetterFailure carries task-classified processing failure metadata.
type DeadLetterFailure struct {
	Class    string    `json:"class"`
	Code     string    `json:"code"`
	Reason   string    `json:"reason"`
	Attempts int       `json:"attempts"`
	FailedAt time.Time `json:"failed_at"`
}

// DeadLetterRecord is the single application consumer DLQ wire contract.
type DeadLetterRecord struct {
	SchemaVersion int               `json:"schema_version"`
	Consumer      string            `json:"consumer"`
	EventID       string            `json:"event_id,omitempty"`
	Source        DeadLetterSource  `json:"source"`
	Failure       DeadLetterFailure `json:"failure"`
}

func ValidateDeadLetterRecord(record DeadLetterRecord) error {
	if record.SchemaVersion != DeadLetterSchemaVersion {
		return fmt.Errorf("unsupported dead letter schema version %d", record.SchemaVersion)
	}
	if strings.TrimSpace(record.Consumer) == "" {
		return errors.New("dead letter consumer is required")
	}
	if strings.TrimSpace(record.Source.Topic) == "" {
		return errors.New("dead letter source topic is required")
	}
	if record.Source.Partition < 0 {
		return errors.New("dead letter source partition must be non-negative")
	}
	if record.Source.Offset < 0 {
		return errors.New("dead letter source offset must be non-negative")
	}
	if strings.TrimSpace(record.Failure.Class) == "" {
		return errors.New("dead letter failure class is required")
	}
	if strings.TrimSpace(record.Failure.Code) == "" {
		return errors.New("dead letter failure code is required")
	}
	if record.Failure.Attempts < 1 {
		return errors.New("dead letter failure attempts must be at least one")
	}
	if record.Failure.FailedAt.IsZero() {
		return errors.New("dead letter failure failed_at is required")
	}
	if len(record.Failure.Reason) > MaxDeadLetterReasonBytes {
		return fmt.Errorf("dead letter failure reason exceeds %d bytes", MaxDeadLetterReasonBytes)
	}
	return nil
}

func EncodeDeadLetterRecord(record DeadLetterRecord) ([]byte, error) {
	record.Failure.Reason = truncateDeadLetterReason(record.Failure.Reason)
	if err := ValidateDeadLetterRecord(record); err != nil {
		return nil, err
	}
	value, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal dead letter record: %w", err)
	}
	return value, nil
}

func DecodeDeadLetterRecord(raw []byte) (DeadLetterRecord, error) {
	var record DeadLetterRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return DeadLetterRecord{}, fmt.Errorf("decode dead letter record: %w", err)
	}
	if err := ValidateDeadLetterRecord(record); err != nil {
		return DeadLetterRecord{}, err
	}
	return record, nil
}

func NewDeadLetterRecord(consumer, eventID string, source kafka.Message, failure DeadLetterFailure) (DeadLetterRecord, error) {
	record := DeadLetterRecord{
		SchemaVersion: DeadLetterSchemaVersion,
		Consumer:      strings.TrimSpace(consumer),
		EventID:       strings.TrimSpace(eventID),
		Source: DeadLetterSource{
			Topic: source.Topic, Partition: source.Partition, Offset: source.Offset,
			Key: cloneDeadLetterBytes(source.Key), Value: cloneDeadLetterBytes(source.Value), Time: source.Time,
		},
		Failure: failure,
	}
	if source.Headers != nil {
		record.Source.Headers = make([]DeadLetterHeader, 0, len(source.Headers))
		for _, header := range source.Headers {
			record.Source.Headers = append(record.Source.Headers, DeadLetterHeader{Key: header.Key, Value: cloneDeadLetterBytes(header.Value)})
		}
	}
	record.Failure.Class = strings.TrimSpace(record.Failure.Class)
	record.Failure.Code = strings.TrimSpace(record.Failure.Code)
	record.Failure.Reason = truncateDeadLetterReason(record.Failure.Reason)
	if record.Failure.FailedAt.IsZero() {
		record.Failure.FailedAt = time.Now().UTC()
	} else {
		record.Failure.FailedAt = record.Failure.FailedAt.UTC()
	}
	if err := ValidateDeadLetterRecord(record); err != nil {
		return DeadLetterRecord{}, err
	}
	return record, nil
}

func BuildDeadLetterMessage(record DeadLetterRecord) (kafka.Message, error) {
	value, err := EncodeDeadLetterRecord(record)
	if err != nil {
		return kafka.Message{}, err
	}
	key := fmt.Sprintf("%s:%s:%d:%d", record.Consumer, record.Source.Topic, record.Source.Partition, record.Source.Offset)
	return kafka.Message{Key: []byte(key), Value: value, Time: record.Failure.FailedAt.UTC()}, nil
}

func cloneDeadLetterBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func truncateDeadLetterReason(reason string) string {
	if len(reason) <= MaxDeadLetterReasonBytes {
		return reason
	}
	end := MaxDeadLetterReasonBytes
	for end > 0 && !utf8.ValidString(reason[:end]) {
		end--
	}
	return reason[:end]
}
