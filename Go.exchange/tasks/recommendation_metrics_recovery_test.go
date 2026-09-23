package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

func recommendationMetricsRecoveryConfig() config.KafkaConfig {
	return config.KafkaConfig{
		RecommendationMetricsGroupID: "recommendation-metrics-recovery-test",
		ConsumerDLQTopic:             "goexchange.consumer.dlq.v1",
	}
}

func mustRecommendationRecoveryMessage(t *testing.T, offset int64, eventID, eventType string) kafka.Message {
	t.Helper()
	at := time.Now().UTC()
	payload := eventing.RecommendationBehaviorPayload{
		UserID: 7, PostID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "ranker_v1", RankerConfigHash: "config-hash", StrategyID: "strategy-a", ReceivedAt: at,
		SelectionMode: eventing.RecommendationSelectionModeRanked,
	}
	event, err := eventing.NewRecommendationBehaviorEnvelope(eventID, eventType, at, payload)
	if err != nil {
		t.Fatal(err)
	}
	return mustRecommendationRecoveryMessageRaw(t, offset, event)
}

func mustRecommendationRecoveryMessageRaw(t *testing.T, offset int64, event eventing.Envelope) kafka.Message {
	t.Helper()
	value, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.recommendation.events.v1", Partition: 0, Offset: offset, Value: value}
}

func TestRecommendationMetricsRecoveryDecoderClassifiesPermanentFailures(t *testing.T) {
	base := func(id, eventType string) eventing.Envelope {
		at := time.Now().UTC()
		payload, err := json.Marshal(eventing.RecommendationBehaviorPayload{
			UserID: 7, PostID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
			RankerVersion: "ranker_v1", RankerConfigHash: "hash", StrategyID: "strategy", ReceivedAt: at,
			SelectionMode: eventing.RecommendationSelectionModeRanked,
		})
		if err != nil {
			t.Fatal(err)
		}
		return eventing.Envelope{ID: id, Type: eventType, SchemaVersion: eventing.RecommendationBehaviorSchemaVersion, OccurredAt: at, Payload: payload}
	}

	badUUID := base("not-a-uuid", eventing.EventTypeRecommendationImpression)
	wrongSchema := base(uuid.NewString(), eventing.EventTypeRecommendationImpression)
	wrongSchema.SchemaVersion--
	unsupportedType := base(uuid.NewString(), "recommendation.unknown")
	missingOccurredAt := base(uuid.NewString(), eventing.EventTypeRecommendationImpression)
	missingOccurredAt.OccurredAt = time.Time{}
	badPayload := base(uuid.NewString(), eventing.EventTypeRecommendationImpression)
	badPayload.Payload = []byte("[]")
	invalidBusiness := base(uuid.NewString(), eventing.EventTypeRecommendationImpression)
	var invalidPayload eventing.RecommendationBehaviorPayload
	if err := json.Unmarshal(invalidBusiness.Payload, &invalidPayload); err != nil {
		t.Fatal(err)
	}
	invalidPayload.Position = 0
	invalidBusiness.Payload, _ = json.Marshal(invalidPayload)

	tests := []struct {
		name string
		msg  kafka.Message
		code string
	}{
		{name: "bad envelope", msg: kafka.Message{Value: []byte("{")}, code: kafkaFailureCodeDecodeEnvelope},
		{name: "invalid uuid", msg: mustRecommendationRecoveryMessageRaw(t, 1, badUUID), code: kafkaFailureCodeInvalidPayload},
		{name: "wrong schema", msg: mustRecommendationRecoveryMessageRaw(t, 2, wrongSchema), code: kafkaFailureCodeUnsupportedSchema},
		{name: "unsupported type", msg: mustRecommendationRecoveryMessageRaw(t, 3, unsupportedType), code: kafkaFailureCodeUnsupportedEvent},
		{name: "missing occurred at", msg: mustRecommendationRecoveryMessageRaw(t, 4, missingOccurredAt), code: kafkaFailureCodeInvalidPayload},
		{name: "bad payload json", msg: mustRecommendationRecoveryMessageRaw(t, 5, badPayload), code: kafkaFailureCodeDecodePayload},
		{name: "invalid business payload", msg: mustRecommendationRecoveryMessageRaw(t, 6, invalidBusiness), code: kafkaFailureCodeInvalidPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRecommendationMetricEvent(test.msg)
			if kafkaFailureClassOf(err) != kafkaFailurePermanent || kafkaFailureCode(err) != test.code {
				t.Fatalf("class=%q code=%q err=%v want code=%q", kafkaFailureClassOf(err), kafkaFailureCode(err), err, test.code)
			}
		})
	}
}

func TestRecommendationMetricsPermanentMessageDoesNotDropFollowingValidMessage(t *testing.T) {
	bad := kafka.Message{Topic: "goexchange.recommendation.events.v1", Partition: 0, Offset: 200, Value: []byte("{malformed")}
	validID := uuid.NewString()
	valid := mustRecommendationRecoveryMessage(t, 201, validID, eventing.EventTypeRecommendationImpression)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, valid}}
	publisher := &fakeRawKafkaMessagePublisher{}
	cfg := recommendationMetricsRecoveryConfig()
	applied := false
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, publisher, nil, cfg, kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, group string, gotConfig config.KafkaConfig, records []recommendationMetricEvent) error {
		applied = true
		if group != cfg.RecommendationMetricsGroupID || gotConfig.ConsumerDLQTopic != cfg.ConsumerDLQTopic {
			t.Fatalf("explicit dependencies group=%q config=%#v", group, gotConfig)
		}
		if len(records) != 1 || records[0].Envelope.ID != validID {
			t.Fatalf("records=%#v", records)
		}
		return nil
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consume error=%v", err)
	}
	if !applied || len(publisher.messages) != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 2 {
		t.Fatalf("applied=%t DLQ=%d commits=%#v", applied, len(publisher.messages), reader.commits)
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(publisher.messages[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	if publisher.topics[0] != cfg.ConsumerDLQTopic || payload.Consumer != kafkaConsumerRecommendationMetrics || payload.SourceOffset != 200 || payload.ErrorCode != kafkaFailureCodeDecodeEnvelope {
		t.Fatalf("DLQ topic=%q payload=%+v", publisher.topics[0], payload)
	}
}

func TestRecommendationMetricsRecoveryDLQFailureDoesNotApplyOrCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{{Topic: "recommendation", Offset: 1, Value: []byte("{")}}}
	publisher := &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")}
	called := false
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, publisher, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error {
		called = true
		return nil
	})
	if called || len(reader.commits) != 0 || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("apply=%t commits=%d class=%q code=%q err=%v", called, len(reader.commits), kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
}

func TestRecommendationMetricsRecoveryRetrySuccessCommitsAfterApply(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustRecommendationRecoveryMessage(t, 1, uuid.NewString(), eventing.EventTypeRecommendationImpression)}}
	applyCalls := 0
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, nil, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error {
		applyCalls++
		if applyCalls < 3 {
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("temporary DB failure"))
		}
		return nil
	})
	if !errors.Is(err, io.EOF) || applyCalls != 3 || len(reader.commits) != 1 {
		t.Fatalf("err=%v apply calls=%d commits=%d", err, applyCalls, len(reader.commits))
	}
}

func TestRecommendationMetricsRecoveryRetryExhaustionLeavesBatchUncommitted(t *testing.T) {
	bad := kafka.Message{Topic: "goexchange.recommendation.events.v1", Partition: 0, Offset: 200, Value: []byte("{")}
	valid := mustRecommendationRecoveryMessage(t, 201, uuid.NewString(), eventing.EventTypeRecommendationImpression)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, valid}}
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, publisher, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 3}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error {
		applyCalls++
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("database down"))
	})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction || applyCalls != 3 || len(reader.commits) != 0 || len(publisher.messages) != 1 {
		t.Fatalf("class=%q code=%q apply calls=%d commits=%d DLQ=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), applyCalls, len(reader.commits), len(publisher.messages), err)
	}
}

func TestRecommendationMetricsRecoveryDeduplicatesValidEventsWithinBatch(t *testing.T) {
	eventID := uuid.NewString()
	message := mustRecommendationRecoveryMessage(t, 1, eventID, eventing.EventTypeRecommendationImpression)
	duplicate := message
	duplicate.Offset = 2
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{message, duplicate}}
	applyCalls := 0
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, nil, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []recommendationMetricEvent) error {
		applyCalls++
		if len(records) != 1 || records[0].Envelope.ID != eventID {
			t.Fatalf("records=%#v", records)
		}
		return nil
	})
	if !errors.Is(err, io.EOF) || applyCalls != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 2 {
		t.Fatalf("err=%v apply calls=%d commits=%#v", err, applyCalls, reader.commits)
	}
}

func TestRecommendationMetricsRecoveryCommitFailureIsClassifiedAndNotDLQed(t *testing.T) {
	reader := &fakeUserBehaviorReader{
		messages:  []kafka.Message{mustRecommendationRecoveryMessage(t, 1, uuid.NewString(), eventing.EventTypeRecommendationImpression)},
		commitErr: errors.New("commit unavailable"),
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeRecommendationMetricsMessagesWithApply(context.Background(), reader, publisher, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error {
		return nil
	})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit || len(publisher.messages) != 0 {
		t.Fatalf("class=%q code=%q DLQ=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), len(publisher.messages), err)
	}
}

func TestRecommendationMetricsRecoveryContextCancellationStopsRetryAndCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustRecommendationRecoveryMessage(t, 1, uuid.NewString(), eventing.EventTypeRecommendationImpression)}}
	publisher := &fakeRawKafkaMessagePublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	applyCalls := 0
	err := consumeRecommendationMetricsMessagesWithApply(ctx, reader, publisher, nil, recommendationMetricsRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error {
		applyCalls++
		cancel()
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("database interrupted"))
	})
	if !errors.Is(err, context.Canceled) || applyCalls != 1 || len(reader.commits) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("err=%v apply calls=%d commits=%d DLQ=%d", err, applyCalls, len(reader.commits), len(publisher.messages))
	}
}

func TestRecommendationMetricsRecoveryNilDatabaseIsRetryable(t *testing.T) {
	message := mustRecommendationRecoveryMessage(t, 1, uuid.NewString(), eventing.EventTypeRecommendationImpression)
	record, err := decodeRecommendationMetricEvent(message)
	if err != nil {
		t.Fatal(err)
	}
	cfg := recommendationMetricsRecoveryConfig()
	err = applyRecommendationMetricRecords(context.Background(), nil, cfg.RecommendationMetricsGroupID, cfg, []recommendationMetricEvent{record})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseUnavailable {
		t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
}
