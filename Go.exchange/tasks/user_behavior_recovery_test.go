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

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

func userBehaviorRecoveryConfig() config.KafkaConfig {
	return config.KafkaConfig{
		UserBehaviorGroupID: "user-behavior-recovery-test",
		ConsumerDLQTopic:    "goexchange.consumer.dlq.v1",
	}
}

func mustUserBehaviorRecoveryMessage(t *testing.T, offset int64, id, eventType string, userID, postID uint, likeVersion int64) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(eventing.UserBehaviorPayload{UserID: userID, PostID: postID, LikeVersion: likeVersion})
	if err != nil {
		t.Fatal(err)
	}
	return mustUserBehaviorRecoveryMessageRaw(t, offset, eventing.Envelope{
		ID: id, Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), Payload: payload,
	})
}

func mustUserBehaviorRecoveryMessageRaw(t *testing.T, offset int64, envelope eventing.Envelope) kafka.Message {
	t.Helper()
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.user.behavior.v1", Partition: 0, Offset: offset, Value: value}
}

func TestUserBehaviorRecoveryDecoderClassifiesPermanentFailures(t *testing.T) {
	wrongSchema := mustUserBehaviorRecoveryMessage(t, 1, "schema", eventing.EventTypePostViewed, 7, 42, 0)
	var wrongSchemaEnvelope eventing.Envelope
	if err := json.Unmarshal(wrongSchema.Value, &wrongSchemaEnvelope); err != nil {
		t.Fatal(err)
	}
	wrongSchemaEnvelope.SchemaVersion = 2
	wrongSchema = mustUserBehaviorRecoveryMessageRaw(t, 1, wrongSchemaEnvelope)

	badPayload := mustUserBehaviorRecoveryMessage(t, 2, "payload", eventing.EventTypePostViewed, 7, 42, 0)
	var badPayloadEnvelope eventing.Envelope
	if err := json.Unmarshal(badPayload.Value, &badPayloadEnvelope); err != nil {
		t.Fatal(err)
	}
	badPayloadEnvelope.Payload = []byte("[]")
	badPayload = mustUserBehaviorRecoveryMessageRaw(t, 2, badPayloadEnvelope)

	invalidRequired := mustUserBehaviorRecoveryMessage(t, 3, "required", eventing.EventTypePostViewed, 0, 42, 0)
	invalidVersion := mustUserBehaviorRecoveryMessage(t, 4, "version", eventing.EventTypePostLiked, 7, 42, 0)
	tests := []struct {
		name string
		msg  kafka.Message
		code string
	}{
		{name: "malformed envelope", msg: kafka.Message{Value: []byte("{")}, code: kafkaFailureCodeDecodeEnvelope},
		{name: "unsupported type", msg: mustUserBehaviorRecoveryMessage(t, 5, "type", "post.deleted", 7, 42, 0), code: kafkaFailureCodeUnsupportedEvent},
		{name: "wrong schema", msg: wrongSchema, code: kafkaFailureCodeUnsupportedSchema},
		{name: "bad payload json", msg: badPayload, code: kafkaFailureCodeDecodePayload},
		{name: "missing required ids", msg: invalidRequired, code: kafkaFailureCodeInvalidPayload},
		{name: "nonpositive like version", msg: invalidVersion, code: kafkaFailureCodeInvalidPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeUserBehaviorMessage(test.msg)
			if kafkaFailureClassOf(err) != kafkaFailurePermanent || kafkaFailureCode(err) != test.code {
				t.Fatalf("class=%q code=%q err=%v want code=%q", kafkaFailureClassOf(err), kafkaFailureCode(err), err, test.code)
			}
		})
	}
}

func TestUserBehaviorPermanentMessageDoesNotDropFollowingValidMessage(t *testing.T) {
	bad := kafka.Message{Topic: "goexchange.user.behavior.v1", Partition: 0, Offset: 100, Value: []byte("{malformed")}
	valid := mustUserBehaviorRecoveryMessage(t, 101, "view-101", eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, valid}}
	publisher := &fakeRawKafkaMessagePublisher{}
	cfg := userBehaviorRecoveryConfig()
	applied := false
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, cfg, kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, group string, gotConfig config.KafkaConfig, records []userBehaviorEventRecord) error {
		applied = true
		if group != cfg.UserBehaviorGroupID || gotConfig.ConsumerDLQTopic != cfg.ConsumerDLQTopic {
			t.Fatalf("explicit dependencies group=%q config=%#v", group, gotConfig)
		}
		if len(records) != 1 || records[0].Envelope.ID != "view-101" {
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
	if publisher.topics[0] != cfg.ConsumerDLQTopic || payload.Consumer != kafkaConsumerUserBehaviorProjection || payload.SourceOffset != 100 || payload.ErrorCode != kafkaFailureCodeDecodeEnvelope {
		t.Fatalf("DLQ topic=%q payload=%+v", publisher.topics[0], payload)
	}
}

func TestUserBehaviorRecoveryDLQFailureDoesNotApplyOrCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{{Topic: "user-behavior", Offset: 1, Value: []byte("{")}}}
	publisher := &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")}
	called := false
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		called = true
		return nil
	})
	if called || len(reader.commits) != 0 || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("apply=%t commits=%d class=%q code=%q err=%v", called, len(reader.commits), kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
}

func TestUserBehaviorRecoveryRetrySuccessCommitsAfterApply(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustUserBehaviorRecoveryMessage(t, 1, "view-retry", eventing.EventTypePostViewed, 7, 42, 0)}}
	applyCalls := 0
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, nil, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
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

func TestUserBehaviorRecoveryRetryExhaustionLeavesWholeBatchUncommitted(t *testing.T) {
	bad := kafka.Message{Topic: "goexchange.user.behavior.v1", Partition: 0, Offset: 100, Value: []byte("{")}
	valid := mustUserBehaviorRecoveryMessage(t, 101, "view-outage", eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, valid}}
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 3}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		applyCalls++
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("database down"))
	})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction || applyCalls != 3 {
		t.Fatalf("class=%q code=%q apply calls=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), applyCalls, err)
	}
	if len(reader.commits) != 0 || len(publisher.messages) != 1 {
		t.Fatalf("commits=%d DLQ=%d; batch must remain uncommitted after DB failure", len(reader.commits), len(publisher.messages))
	}
}

func TestUserBehaviorRecoveryDeduplicatesValidEventsWithinBatch(t *testing.T) {
	message := mustUserBehaviorRecoveryMessage(t, 1, "view-duplicate", eventing.EventTypePostViewed, 7, 42, 0)
	duplicate := message
	duplicate.Offset = 2
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{message, duplicate}}
	applyCalls := 0
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, nil, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
		applyCalls++
		if len(records) != 1 || records[0].Envelope.ID != "view-duplicate" {
			t.Fatalf("records=%#v", records)
		}
		return nil
	})
	if !errors.Is(err, io.EOF) || applyCalls != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 2 {
		t.Fatalf("err=%v apply calls=%d commits=%#v", err, applyCalls, reader.commits)
	}
}

func TestUserBehaviorRecoveryCommitFailureIsClassifiedAndNotDLQed(t *testing.T) {
	reader := &fakeUserBehaviorReader{
		messages:  []kafka.Message{mustUserBehaviorRecoveryMessage(t, 1, "view-commit", eventing.EventTypePostViewed, 7, 42, 0)},
		commitErr: errors.New("commit unavailable"),
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		return nil
	})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit || len(publisher.messages) != 0 {
		t.Fatalf("class=%q code=%q DLQ=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), len(publisher.messages), err)
	}
}

func TestUserBehaviorRecoveryContextCancellationStopsRetryAndCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustUserBehaviorRecoveryMessage(t, 1, "view-cancel", eventing.EventTypePostViewed, 7, 42, 0)}}
	publisher := &fakeRawKafkaMessagePublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	applyCalls := 0
	err := consumeUserBehaviorMessagesWithApply(ctx, reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 5}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		applyCalls++
		cancel()
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("database interrupted"))
	})
	if !errors.Is(err, context.Canceled) || applyCalls != 1 || len(reader.commits) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("err=%v apply calls=%d commits=%d DLQ=%d", err, applyCalls, len(reader.commits), len(publisher.messages))
	}
}

func TestUserBehaviorRecoveryNilDatabaseIsRetryable(t *testing.T) {
	message := mustUserBehaviorRecoveryMessage(t, 1, "view-no-db", eventing.EventTypePostViewed, 7, 42, 0)
	record, err := decodeUserBehaviorMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	err = applyUserBehaviorRecords(context.Background(), nil, userBehaviorRecoveryConfig().UserBehaviorGroupID, userBehaviorRecoveryConfig(), []userBehaviorEventRecord{record})
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseUnavailable {
		t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
}
