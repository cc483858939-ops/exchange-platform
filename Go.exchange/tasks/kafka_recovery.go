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
	kafkaFailureCodeDecodeEnvelope          = "decode_envelope"
	kafkaFailureCodeUnsupportedEvent        = "unsupported_event_type"
	kafkaFailureCodeUnsupportedSchema       = "unsupported_schema"
	kafkaFailureCodeDecodePayload           = "decode_payload"
	kafkaFailureCodeInvalidPayload          = "invalid_payload"
	kafkaFailureCodeDatabaseUnavailable     = "database_unavailable"
	kafkaFailureCodeDatabaseTransaction     = "database_transaction"
	kafkaFailureCodeDLQPublish              = "dlq_publish"
	kafkaFailureCodeKafkaCommit             = "kafka_commit"
	kafkaFailureCodeProviderRetryable       = "provider_retryable"
	kafkaFailureCodeProviderPermanent       = "provider_permanent"
	kafkaFailureCodeProviderContractInvalid = "provider_contract_invalid"
	kafkaFailureCodeSourceChanged           = "source_changed"
	kafkaFailureCodeInternalState           = "internal_state"
	kafkaRecoveryCodeNone                   = "none"
)

const (
	kafkaConsumerLikeSnapshotProjection    = "like_snapshot_projection"
	kafkaConsumerUserBehaviorProjection    = "user_behavior_projection"
	kafkaConsumerRecommendationMetrics     = "recommendation_metrics"
	kafkaConsumerPostEmbedding             = "post_embedding"
	kafkaConsumerNotificationProjection    = "notification_projection"
	kafkaRecoveryOutcomeMessageApplied     = "message_applied"
	kafkaRecoveryOutcomeBatchApplied       = "batch_applied"
	kafkaRecoveryOutcomeMessageNoop        = "message_noop"
	kafkaRecoveryOutcomeRetryAttempt       = "retry_attempt"
	kafkaRecoveryOutcomeRetryExhausted     = "retry_exhausted"
	kafkaRecoveryOutcomeMessageDLQ         = "message_dlq"
	kafkaRecoveryOutcomeDLQPublishFailed   = "dlq_publish_failed"
	kafkaRecoveryOutcomeRedeliveryRequired = "redelivery_required"
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

func retryKafkaOperation(ctx context.Context, policy kafkaRetryPolicy, onRetry func(attempt int, err error), operation func() error) error {
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
		if err := ctx.Err(); err != nil {
			return err
		}
		if onRetry != nil {
			onRetry(attempt+1, lastErr)
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

type rawKafkaMessagePublisher = eventing.RawPublisher

type eventingRawKafkaMessagePublisher struct {
	kafkaConfig config.KafkaConfig
}

func (p eventingRawKafkaMessagePublisher) PublishRaw(ctx context.Context, topic string, messages ...kafka.Message) error {
	return eventing.PublishRawMessages(ctx, p.kafkaConfig, topic, messages...)
}

func deadLetterFailureFromProcessError(err error, attempts int, failedAt time.Time) (eventing.DeadLetterFailure, error) {
	if err == nil {
		return eventing.DeadLetterFailure{}, errors.New("consumer DLQ failure is required")
	}
	class := kafkaFailureClassOf(err)
	code := kafkaFailureCode(err)
	if class == "" || code == "" {
		return eventing.DeadLetterFailure{}, errors.New("consumer DLQ failure must have a classified error and stable code")
	}
	if attempts < 1 {
		attempts = 1
	}
	if failedAt.IsZero() {
		failedAt = time.Now().UTC()
	} else {
		failedAt = failedAt.UTC()
	}
	return eventing.DeadLetterFailure{Class: string(class), Code: code, Reason: err.Error(), Attempts: attempts, FailedAt: failedAt}, nil
}

func buildClassifiedDeadLetterMessage(consumer string, source kafka.Message, processErr error, attempts int, failedAt time.Time) (kafka.Message, error) {
	if processErr == nil {
		return kafka.Message{}, errors.New("consumer DLQ failure is required")
	}
	failure, err := deadLetterFailureFromProcessError(processErr, attempts, failedAt)
	if err != nil {
		return kafka.Message{}, err
	}
	record, err := eventing.NewDeadLetterRecord(consumer, bestEffortKafkaEventID(source.Value), source, failure)
	if err != nil {
		return kafka.Message{}, err
	}
	return eventing.BuildDeadLetterMessage(record)
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
	dlqMessage, err := buildClassifiedDeadLetterMessage(consumer, source, processErr, attempts, time.Now().UTC())
	if err != nil {
		return retryableKafkaError(kafkaFailureCodeDLQPublish, err)
	}
	if err := publisher.PublishRaw(ctx, topic, dlqMessage); err != nil {
		return retryableKafkaError(kafkaFailureCodeDLQPublish, err)
	}
	return nil
}
