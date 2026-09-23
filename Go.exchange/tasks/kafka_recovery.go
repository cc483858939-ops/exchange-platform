package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
)

type kafkaFailureClass string

const (
	kafkaFailurePermanent kafkaFailureClass = "permanent"
	kafkaFailureRetryable kafkaFailureClass = "retryable"
)

const (
	kafkaFailureCodeDecodeEnvelope      = "decode_envelope"
	kafkaFailureCodeUnsupportedEvent    = "unsupported_event_type"
	kafkaFailureCodeUnsupportedSchema   = "unsupported_schema"
	kafkaFailureCodeDecodePayload       = "decode_payload"
	kafkaFailureCodeInvalidPayload      = "invalid_payload"
	kafkaFailureCodeDatabaseUnavailable = "database_unavailable"
	kafkaFailureCodeDatabaseTransaction = "database_transaction"
	kafkaFailureCodeDLQPublish          = "dlq_publish"
	kafkaFailureCodeKafkaCommit         = "kafka_commit"
	kafkaRecoveryCodeNone               = "none"
)

const (
	kafkaConsumerLikeSnapshotProjection  = "like_snapshot_projection"
	kafkaConsumerUserBehaviorProjection  = "user_behavior_projection"
	kafkaConsumerRecommendationMetrics   = "recommendation_metrics"
	kafkaRecoveryOutcomeApplied          = "applied"
	kafkaRecoveryOutcomeNoop             = "noop"
	kafkaRecoveryOutcomeRetry            = "retry"
	kafkaRecoveryOutcomeRetryExhausted   = "retry_exhausted"
	kafkaRecoveryOutcomeDLQ              = "dlq"
	kafkaRecoveryOutcomeDLQPublishFailed = "dlq_publish_failed"
)

type kafkaProcessError struct {
	Class kafkaFailureClass
	Code  string
	Err   error
}

func (e *kafkaProcessError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return fmt.Sprintf("%s: %v", e.Class, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e *kafkaProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func permanentKafkaError(code string, err error) error {
	if err == nil {
		return nil
	}
	return &kafkaProcessError{Class: kafkaFailurePermanent, Code: code, Err: err}
}

func retryableKafkaError(code string, err error) error {
	if err == nil {
		return nil
	}
	return &kafkaProcessError{Class: kafkaFailureRetryable, Code: code, Err: err}
}

func kafkaFailureClassOf(err error) kafkaFailureClass {
	var processErr *kafkaProcessError
	if errors.As(err, &processErr) && processErr != nil {
		return processErr.Class
	}
	return ""
}

func kafkaFailureCode(err error) string {
	var processErr *kafkaProcessError
	if errors.As(err, &processErr) && processErr != nil {
		return processErr.Code
	}
	return ""
}

type kafkaRetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

var defaultKafkaRetryPolicy = kafkaRetryPolicy{
	MaxAttempts:    5,
	InitialBackoff: 250 * time.Millisecond,
	MaxBackoff:     2 * time.Second,
}

func retryKafkaOperation(ctx context.Context, policy kafkaRetryPolicy, operation func() error) error {
	if ctx == nil {
		return errors.New("Kafka retry context is nil")
	}
	if operation == nil {
		return errors.New("Kafka retry operation is nil")
	}
	if policy.MaxAttempts < 1 || policy.InitialBackoff < 0 || policy.MaxBackoff < 0 {
		return errors.New("Kafka retry policy values must be non-negative and MaxAttempts must be positive")
	}

	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if kafkaFailureClassOf(lastErr) != kafkaFailureRetryable || attempt == policy.MaxAttempts {
			return lastErr
		}
		if err := waitForKafkaRetry(ctx, kafkaRetryBackoff(policy, attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

func kafkaRetryBackoff(policy kafkaRetryPolicy, failedAttempt int) time.Duration {
	delay := policy.InitialBackoff
	for attempt := 1; attempt < failedAttempt; attempt++ {
		if policy.MaxBackoff > 0 && delay >= policy.MaxBackoff/2 {
			return policy.MaxBackoff
		}
		delay *= 2
	}
	if policy.MaxBackoff > 0 && delay > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return delay
}

func waitForKafkaRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

type consumerDLQPayload struct {
	Consumer        string              `json:"consumer"`
	SourceTopic     string              `json:"source_topic"`
	SourcePartition int                 `json:"source_partition"`
	SourceOffset    int64               `json:"source_offset"`
	SourceKey       []byte              `json:"source_key"`
	SourceValue     []byte              `json:"source_value"`
	SourceTime      time.Time           `json:"source_time,omitempty"`
	SourceHeaders   []consumerDLQHeader `json:"source_headers,omitempty"`
	EventID         string              `json:"event_id,omitempty"`
	ErrorClass      string              `json:"error_class"`
	ErrorCode       string              `json:"error_code"`
	Reason          string              `json:"reason"`
	Attempts        int                 `json:"attempts"`
	FailedAt        time.Time           `json:"failed_at"`
}

type consumerDLQHeader struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type rawKafkaMessagePublisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}

type eventingRawKafkaMessagePublisher struct {
	kafkaConfig config.KafkaConfig
}

func (p eventingRawKafkaMessagePublisher) PublishRaw(ctx context.Context, topic string, messages ...kafka.Message) error {
	return eventing.PublishRawMessages(ctx, p.kafkaConfig, topic, messages...)
}

func buildConsumerDLQMessage(consumer string, source kafka.Message, processErr error, attempts int, failedAt time.Time) (kafka.Message, error) {
	if processErr == nil {
		return kafka.Message{}, errors.New("consumer DLQ failure is required")
	}
	if kafkaFailureClassOf(processErr) == "" || kafkaFailureCode(processErr) == "" {
		return kafka.Message{}, errors.New("consumer DLQ failure must have a classified error and stable code")
	}
	if attempts < 1 {
		attempts = 1
	}
	if failedAt.IsZero() {
		failedAt = time.Now().UTC()
	} else {
		failedAt = failedAt.UTC()
	}

	payload := consumerDLQPayload{
		Consumer: consumer, SourceTopic: source.Topic, SourcePartition: source.Partition, SourceOffset: source.Offset,
		SourceKey: cloneKafkaBytes(source.Key), SourceValue: cloneKafkaBytes(source.Value), SourceTime: source.Time,
		EventID: bestEffortKafkaEventID(source.Value), ErrorClass: string(kafkaFailureClassOf(processErr)),
		ErrorCode: kafkaFailureCode(processErr), Reason: processErr.Error(), Attempts: attempts, FailedAt: failedAt,
	}
	if len(source.Headers) > 0 {
		payload.SourceHeaders = make([]consumerDLQHeader, 0, len(source.Headers))
		for _, header := range source.Headers {
			payload.SourceHeaders = append(payload.SourceHeaders, consumerDLQHeader{Key: header.Key, Value: cloneKafkaBytes(header.Value)})
		}
	}

	value, err := json.Marshal(payload)
	if err != nil {
		return kafka.Message{}, fmt.Errorf("marshal consumer DLQ payload: %w", err)
	}
	key := fmt.Sprintf("%s:%s:%d:%d", consumer, source.Topic, source.Partition, source.Offset)
	return kafka.Message{Key: []byte(key), Value: value, Time: failedAt}, nil
}

func cloneKafkaBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func bestEffortKafkaEventID(raw []byte) string {
	if !utf8.Valid(raw) {
		return ""
	}
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.ID)
}

func publishConsumerDLQ(ctx context.Context, publisher rawKafkaMessagePublisher, topic, consumer string, source kafka.Message, processErr error, attempts int) error {
	if ctx == nil {
		return errors.New("consumer DLQ context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if publisher == nil {
		return retryableKafkaError(kafkaFailureCodeDLQPublish, errors.New("consumer DLQ publisher is unavailable"))
	}
	dlqMessage, err := buildConsumerDLQMessage(consumer, source, processErr, attempts, time.Now().UTC())
	if err != nil {
		return retryableKafkaError(kafkaFailureCodeDLQPublish, err)
	}
	if err := publisher.PublishRaw(ctx, topic, dlqMessage); err != nil {
		return retryableKafkaError(kafkaFailureCodeDLQPublish, err)
	}
	return nil
}
