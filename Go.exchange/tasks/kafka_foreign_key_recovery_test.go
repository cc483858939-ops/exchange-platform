package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

func testPostgresForeignKeyError(constraint string) error {
	return fmt.Errorf("projection transaction: %w", &pgconn.PgError{Code: "23503", ConstraintName: constraint})
}

func TestKafkaPostgresForeignKeyClassificationIsStructuralAndAllowlisted(t *testing.T) {
	known := testPostgresForeignKeyError("fk_post_behaviors_post")
	if !kafkaPostgresForeignKeyIsAllowed(known, "fk_post_behaviors_post") {
		t.Fatal("wrapped PostgreSQL 23503 with an allowlisted constraint was not recognized")
	}
	if kafkaPostgresForeignKeyIsAllowed(testPostgresForeignKeyError("fk_unknown"), "fk_post_behaviors_post") {
		t.Fatal("unknown foreign-key constraint was allowlisted")
	}
	if kafkaPostgresForeignKeyIsAllowed(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", ConstraintName: "fk_post_behaviors_post"}), "fk_post_behaviors_post") {
		t.Fatal("unique violation was misclassified as a foreign-key violation")
	}
	if kafkaPostgresForeignKeyIsAllowed(errors.New("foreign key violation 23503"), "fk_post_behaviors_post") {
		t.Fatal("string-matched error was misclassified as a PostgreSQL foreign-key violation")
	}
	for _, code := range []string{"23505", "40001", "57014"} {
		if kafkaPostgresForeignKeyIsAllowed(&pgconn.PgError{Code: code, ConstraintName: "fk_post_behaviors_post"}, "fk_post_behaviors_post") {
			t.Fatalf("PostgreSQL error code %s was misclassified as a foreign-key violation", code)
		}
	}
}

func TestApplyKafkaBatchWithForeignKeyIsolationPreservesOrderAndBoundsCalls(t *testing.T) {
	var applied []string
	var dlq []string
	applyCalls := 0
	err := applyKafkaBatchWithForeignKeyIsolation(context.Background(), []string{"A", "B", "C"},
		func(_ context.Context, batch []string) error {
			applyCalls++
			for _, item := range batch {
				if item == "B" {
					return testPostgresForeignKeyError("fk_post_behaviors_post")
				}
			}
			applied = append(applied, batch...)
			return nil
		},
		func(err error) bool { return kafkaPostgresForeignKeyIsAllowed(err, "fk_post_behaviors_post") },
		func(_ context.Context, item string, _ error) error {
			dlq = append(dlq, item)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []string{"A", "C"}) || !reflect.DeepEqual(dlq, []string{"B"}) {
		t.Fatalf("applied=%v DLQ=%v", applied, dlq)
	}
	if applyCalls != 5 || applyCalls > 2*3-1 {
		t.Fatalf("apply calls=%d; expected ordered bisection within 2n-1 bound", applyCalls)
	}
}

func TestApplyKafkaBatchWithForeignKeyIsolationDoesNotSplitUnknownConstraint(t *testing.T) {
	applyCalls, handlerCalls := 0, 0
	err := applyKafkaBatchWithForeignKeyIsolation(context.Background(), []string{"A", "B", "C"},
		func(context.Context, []string) error {
			applyCalls++
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_unrecognized"))
		},
		func(err error) bool { return kafkaPostgresForeignKeyIsAllowed(err, "fk_post_behaviors_post") },
		func(context.Context, string, error) error {
			handlerCalls++
			return nil
		},
	)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || applyCalls != 1 || handlerCalls != 0 {
		t.Fatalf("class=%q apply calls=%d handler calls=%d err=%v", kafkaFailureClassOf(err), applyCalls, handlerCalls, err)
	}
}

func TestUserBehaviorForeignKeyIsolationDLQsOnlyBadRecordAndCommitsBatch(t *testing.T) {
	first := mustUserBehaviorRecoveryMessage(t, 10, "view-a", eventing.EventTypePostViewed, 7, 42, 0)
	bad := mustUserBehaviorRecoveryMessage(t, 11, "like-b", eventing.EventTypePostLiked, 7, 99, 3)
	bad.Key = []byte("bad-key")
	bad.Headers = []kafka.Header{{Key: "trace", Value: []byte("bad-trace")}}
	bad.Time = time.Date(2026, 10, 13, 7, 8, 9, 10, time.UTC)
	last := mustUserBehaviorRecoveryMessage(t, 12, "view-c", eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{first, bad, last}}
	publisher := &fakeRawKafkaMessagePublisher{}
	var applied []string
	apply := func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
		for _, record := range records {
			if record.Payload.PostID == 99 {
				return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_reaction_post"))
			}
		}
		for _, record := range records {
			applied = append(applied, record.Envelope.ID)
		}
		return nil
	}
	lookup := func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
		if table != "posts" || id != 99 {
			t.Fatalf("unexpected lookup target %s/%d", table, id)
		}
		return false, nil
	}
	cfg := userBehaviorRecoveryConfig()
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, cfg, kafkaRetryPolicy{MaxAttempts: 1}, apply, lookup)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consume error=%v", err)
	}
	if !reflect.DeepEqual(applied, []string{"view-a", "view-c"}) {
		t.Fatalf("applied in order=%v", applied)
	}
	if len(publisher.messages) != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 3 {
		t.Fatalf("DLQ=%d commits=%#v", len(publisher.messages), reader.commits)
	}
	deadLetter, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.topics[0] != cfg.ConsumerDLQTopic || deadLetter.Failure.Class != string(kafkaFailurePermanent) || deadLetter.Failure.Code != kafkaFailureCodeDatabaseForeignKey {
		t.Fatalf("topic=%q failure=%+v", publisher.topics[0], deadLetter.Failure)
	}
	if deadLetter.Source.Topic != bad.Topic || deadLetter.Source.Partition != bad.Partition || deadLetter.Source.Offset != bad.Offset ||
		!bytes.Equal(deadLetter.Source.Key, bad.Key) || !bytes.Equal(deadLetter.Source.Value, bad.Value) || len(deadLetter.Source.Headers) != 1 ||
		deadLetter.Source.Headers[0].Key != bad.Headers[0].Key || !bytes.Equal(deadLetter.Source.Headers[0].Value, bad.Headers[0].Value) || !deadLetter.Source.Time.Equal(bad.Time) {
		t.Fatalf("DLQ source metadata does not match original Kafka message: %+v", deadLetter.Source)
	}
}

func TestUserBehaviorForeignKeyDLQFailureStopsBeforeLaterRecordsAndCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{
		mustUserBehaviorRecoveryMessage(t, 20, "view-before", eventing.EventTypePostViewed, 7, 42, 0),
		mustUserBehaviorRecoveryMessage(t, 21, "like-invalid", eventing.EventTypePostLiked, 7, 99, 4),
		mustUserBehaviorRecoveryMessage(t, 22, "view-after", eventing.EventTypePostViewed, 7, 42, 0),
	}}
	publisher := &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")}
	var applied []string
	apply := func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
		for _, record := range records {
			if record.Payload.PostID == 99 {
				return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_reaction_post"))
			}
		}
		for _, record := range records {
			applied = append(applied, record.Envelope.ID)
		}
		return nil
	}
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, apply,
		func(context.Context, *gorm.DB, string, uint) (bool, error) { return false, nil })
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("error class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	if !reflect.DeepEqual(applied, []string{"view-before"}) || len(reader.commits) != 0 {
		t.Fatalf("applied=%v commits=%d; later events must stop and batch remain uncommitted", applied, len(reader.commits))
	}
}

func TestUserBehaviorForeignKeyRecoveryHandlesMultiplePoisonMessages(t *testing.T) {
	messages := []kafka.Message{
		mustUserBehaviorRecoveryMessage(t, 50, "view-a", eventing.EventTypePostViewed, 7, 42, 0),
		mustUserBehaviorRecoveryMessage(t, 51, "like-b", eventing.EventTypePostLiked, 7, 99, 5),
		mustUserBehaviorRecoveryMessage(t, 52, "view-c", eventing.EventTypePostViewed, 7, 42, 0),
		mustUserBehaviorRecoveryMessage(t, 53, "like-d", eventing.EventTypePostLiked, 7, 100, 6),
		mustUserBehaviorRecoveryMessage(t, 54, "view-e", eventing.EventTypePostViewed, 7, 42, 0),
	}
	reader := &fakeUserBehaviorReader{messages: messages}
	publisher := &fakeRawKafkaMessagePublisher{}
	var applied []string
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1},
		func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
			for _, record := range records {
				if record.Payload.PostID == 99 || record.Payload.PostID == 100 {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_reaction_post"))
				}
			}
			for _, record := range records {
				applied = append(applied, record.Envelope.ID)
			}
			return nil
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			if table != "posts" || (id != 99 && id != 100) {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return false, nil
		},
	)
	if !errors.Is(err, io.EOF) || !reflect.DeepEqual(applied, []string{"view-a", "view-c", "view-e"}) {
		t.Fatalf("err=%v applied=%v", err, applied)
	}
	if len(publisher.messages) != 2 || len(reader.commits) != 1 || len(reader.commits[0]) != len(messages) {
		t.Fatalf("DLQ=%d commits=%#v", len(publisher.messages), reader.commits)
	}
	var offsets []int64
	for _, message := range publisher.messages {
		deadLetter, decodeErr := eventing.DecodeDeadLetterRecord(message.Value)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		offsets = append(offsets, deadLetter.Source.Offset)
	}
	if !reflect.DeepEqual(offsets, []int64{51, 53}) {
		t.Fatalf("DLQ offsets=%v want=[51 53]", offsets)
	}
}

func TestUserBehaviorForeignKeyAlreadyDLQedMessageIsNotRepublishedOnRetry(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{
		mustUserBehaviorRecoveryMessage(t, 60, "view-before", eventing.EventTypePostViewed, 7, 42, 0),
		mustUserBehaviorRecoveryMessage(t, 61, "like-invalid", eventing.EventTypePostLiked, 7, 99, 7),
		mustUserBehaviorRecoveryMessage(t, 62, "view-after", eventing.EventTypePostViewed, 7, 42, 0),
	}}
	publisher := &fakeRawKafkaMessagePublisher{}
	transientFailures := 0
	applyCalls := 0
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 2, InitialBackoff: 0, MaxBackoff: 0},
		func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
			applyCalls++
			for _, record := range records {
				if record.Payload.PostID == 99 {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_reaction_post"))
				}
				if record.Envelope.ID == "view-after" {
					transientFailures++
					if transientFailures == 1 {
						return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("temporary projection outage"))
					}
				}
			}
			return nil
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			if table != "posts" || id != 99 {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return false, nil
		},
	)
	if !errors.Is(err, io.EOF) || applyCalls == 0 || len(publisher.messages) != 1 || len(reader.commits) != 1 {
		t.Fatalf("err=%v apply calls=%d DLQ=%d commits=%d", err, applyCalls, len(publisher.messages), len(reader.commits))
	}
}

func TestUserBehaviorViewWithMissingPostDLQsOnlyBadRecordAndCommitsBatch(t *testing.T) {
	first := mustUserBehaviorRecoveryMessage(t, 30, "view-before-missing-post", eventing.EventTypePostViewed, 7, 42, 0)
	bad := mustUserBehaviorRecoveryMessage(t, 31, "view-missing-post", eventing.EventTypePostViewed, 7, 999, 0)
	bad.Key = []byte("missing-post-view")
	bad.Headers = []kafka.Header{{Key: "trace", Value: []byte("view-trace")}}
	bad.Time = time.Date(2026, 10, 13, 7, 8, 9, 10, time.UTC)
	last := mustUserBehaviorRecoveryMessage(t, 32, "view-after-missing-post", eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{first, bad, last}}
	publisher := &fakeRawKafkaMessagePublisher{}
	var applied []string
	lookupCalls := 0
	cfg := userBehaviorRecoveryConfig()
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, cfg, kafkaRetryPolicy{MaxAttempts: 1},
		func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
			for _, record := range records {
				if record.Payload.PostID == 999 {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_behaviors_post"))
				}
			}
			for _, record := range records {
				applied = append(applied, record.Envelope.ID)
			}
			return nil
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			lookupCalls++
			if table != "posts" || id != 999 {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return false, nil
		},
	)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consume error=%v", err)
	}
	if !reflect.DeepEqual(applied, []string{"view-before-missing-post", "view-after-missing-post"}) {
		t.Fatalf("applied events=%v", applied)
	}
	if lookupCalls != 1 || len(publisher.messages) != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 3 {
		t.Fatalf("parent lookups=%d DLQ=%d commits=%#v", lookupCalls, len(publisher.messages), reader.commits)
	}
	deadLetter, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.topics[0] != cfg.ConsumerDLQTopic || deadLetter.Failure.Class != string(kafkaFailurePermanent) || deadLetter.Failure.Code != kafkaFailureCodeDatabaseForeignKey {
		t.Fatalf("topic=%q failure=%+v", publisher.topics[0], deadLetter.Failure)
	}
	if deadLetter.Source.Topic != bad.Topic || deadLetter.Source.Partition != bad.Partition || deadLetter.Source.Offset != bad.Offset ||
		!bytes.Equal(deadLetter.Source.Key, bad.Key) || !bytes.Equal(deadLetter.Source.Value, bad.Value) || len(deadLetter.Source.Headers) != 1 ||
		deadLetter.Source.Headers[0].Key != bad.Headers[0].Key || !bytes.Equal(deadLetter.Source.Headers[0].Value, bad.Headers[0].Value) || !deadLetter.Source.Time.Equal(bad.Time) {
		t.Fatalf("DLQ source metadata does not match original Kafka message: %+v", deadLetter.Source)
	}
}

func TestUserBehaviorViewWithExistingPostRemainsRetryable(t *testing.T) {
	message := mustUserBehaviorRecoveryMessage(t, 33, "view-existing-post", eventing.EventTypePostViewed, 7, 999, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{message}}
	publisher := &fakeRawKafkaMessagePublisher{}
	lookupCalls, applyCalls := 0, 0
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1},
		func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
			applyCalls++
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_behaviors_post"))
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			lookupCalls++
			if table != "posts" || id != 999 {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return true, nil
		},
	)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction || applyCalls != 1 || lookupCalls != 1 {
		t.Fatalf("class=%q code=%q apply=%d lookup=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), applyCalls, lookupCalls, err)
	}
	if len(publisher.messages) != 0 || len(reader.commits) != 0 {
		t.Fatalf("DLQ=%d commits=%d; an existing Post must remain retryable", len(publisher.messages), len(reader.commits))
	}
}

func TestUserBehaviorViewParentLookupFailureRemainsRetryable(t *testing.T) {
	message := mustUserBehaviorRecoveryMessage(t, 34, "view-parent-lookup-failed", eventing.EventTypePostViewed, 7, 999, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{message}}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1},
		func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_behaviors_post"))
		},
		func(context.Context, *gorm.DB, string, uint) (bool, error) {
			return false, errors.New("temporary PostgreSQL outage")
		},
	)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseUnavailable {
		t.Fatalf("class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	if len(publisher.messages) != 0 || len(reader.commits) != 0 {
		t.Fatalf("DLQ=%d commits=%d; lookup failures must remain retryable", len(publisher.messages), len(reader.commits))
	}
}

func TestUserBehaviorViewDLQFailureStopsBeforeLaterRecordsAndCommit(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{
		mustUserBehaviorRecoveryMessage(t, 35, "view-before-dlq-failure", eventing.EventTypePostViewed, 7, 42, 0),
		mustUserBehaviorRecoveryMessage(t, 36, "view-missing-post-dlq-failure", eventing.EventTypePostViewed, 7, 999, 0),
		mustUserBehaviorRecoveryMessage(t, 37, "view-after-dlq-failure", eventing.EventTypePostViewed, 7, 42, 0),
	}}
	publisher := &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")}
	var applied []string
	err := consumeUserBehaviorMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1},
		func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
			for _, record := range records {
				if record.Payload.PostID == 999 {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_behaviors_post"))
				}
			}
			for _, record := range records {
				applied = append(applied, record.Envelope.ID)
			}
			return nil
		},
		func(context.Context, *gorm.DB, string, uint) (bool, error) { return false, nil },
	)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("error class=%q code=%q err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
	}
	if !reflect.DeepEqual(applied, []string{"view-before-dlq-failure"}) || len(reader.commits) != 0 {
		t.Fatalf("applied=%v commits=%d; later events must stop and batch remain uncommitted", applied, len(reader.commits))
	}
}

func TestRecommendationMetricsForeignKeyIsolationPreservesOtherEvents(t *testing.T) {
	first := mustRecommendationRecoveryMessage(t, 40, uuid.NewString(), eventing.EventTypeRecommendationClick)
	bad := mustRecommendationRecoveryMessage(t, 41, uuid.NewString(), eventing.EventTypeRecommendationClick)
	var badEnvelope eventing.Envelope
	if err := json.Unmarshal(bad.Value, &badEnvelope); err != nil {
		t.Fatal(err)
	}
	var badPayload eventing.RecommendationBehaviorPayload
	if err := json.Unmarshal(badEnvelope.Payload, &badPayload); err != nil {
		t.Fatal(err)
	}
	badPayload.PostID = 777
	var err error
	badEnvelope.Payload, err = json.Marshal(badPayload)
	if err != nil {
		t.Fatal(err)
	}
	bad.Value, err = json.Marshal(badEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	last := mustRecommendationRecoveryMessage(t, 42, uuid.NewString(), eventing.EventTypeRecommendationClick)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{first, bad, last}}
	publisher := &fakeRawKafkaMessagePublisher{}
	var applied []string
	cfg := recommendationMetricsRecoveryConfig()
	err = consumeRecommendationMetricsMessagesWithForeignKeyLookup(context.Background(), reader, publisher, nil, cfg, kafkaRetryPolicy{MaxAttempts: 1},
		func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []recommendationMetricEvent) error {
			for _, record := range records {
				if record.Payload.PostID == 777 {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, testPostgresForeignKeyError("fk_post_behaviors_post"))
				}
			}
			for _, record := range records {
				applied = append(applied, record.Envelope.ID)
			}
			return nil
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			if table != "posts" || id != 777 {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return false, nil
		},
	)
	var firstEnvelope, lastEnvelope eventing.Envelope
	if err := json.Unmarshal(first.Value, &firstEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(last.Value, &lastEnvelope); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(err, io.EOF) || !reflect.DeepEqual(applied, []string{firstEnvelope.ID, lastEnvelope.ID}) {
		t.Fatalf("err=%v applied=%v", err, applied)
	}
	if len(publisher.messages) != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 3 {
		t.Fatalf("DLQ=%d commits=%#v", len(publisher.messages), reader.commits)
	}
	dlq, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if dlq.Source.Offset != bad.Offset || dlq.Failure.Code != kafkaFailureCodeDatabaseForeignKey {
		t.Fatalf("DLQ source=%+v failure=%+v", dlq.Source, dlq.Failure)
	}
}

func TestNotificationForeignKeyRecoveryReappliesFilterBeforeDLQ(t *testing.T) {
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ConsumerDLQTopic: "goexchange.consumer.dlq.v1"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	now := time.Now().UTC()
	envelope, err := eventing.NewUserFollowCreatedEnvelope(uuid.NewString(), eventing.UserFollowCreatedPayload{
		FollowID: 801, FollowerID: 802, FollowingID: 803, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := notificationMessage(t, envelope)
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls, lookupCalls := 0, 0
	err = processNotificationBatchWithForeignKeyRecovery(context.Background(), []kafka.Message{message}, publisher, kafkaRetryPolicy{MaxAttempts: 1},
		func(context.Context, []notificationActivityRecord) error {
			applyCalls++
			if applyCalls == 1 {
				return testPostgresForeignKeyError("fk_notifications_recipient")
			}
			return nil // The existing filter now drops the vanished candidate and commits Inbox.
		},
		func(context.Context, *gorm.DB, string, uint) (bool, error) {
			lookupCalls++
			return false, nil
		},
	)
	if err != nil || applyCalls != 2 || lookupCalls != 0 || len(publisher.messages) != 0 {
		t.Fatalf("err=%v applies=%d lookups=%d DLQ=%d", err, applyCalls, lookupCalls, len(publisher.messages))
	}
}

func TestNotificationForeignKeyRecoveryDLQsOnlyAfterSecondFailureAndMissingParent(t *testing.T) {
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ConsumerDLQTopic: "goexchange.consumer.dlq.v1"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	now := time.Now().UTC()
	envelope, err := eventing.NewUserFollowCreatedEnvelope(uuid.NewString(), eventing.UserFollowCreatedPayload{
		FollowID: 901, FollowerID: 902, FollowingID: 903, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := notificationMessage(t, envelope)
	publisher := &fakeRawKafkaMessagePublisher{}
	applyCalls := 0
	err = processNotificationBatchWithForeignKeyRecovery(context.Background(), []kafka.Message{message}, publisher, kafkaRetryPolicy{MaxAttempts: 1},
		func(context.Context, []notificationActivityRecord) error {
			applyCalls++
			return testPostgresForeignKeyError("fk_notifications_recipient")
		},
		func(_ context.Context, _ *gorm.DB, table string, id uint) (bool, error) {
			if table != "users" || id != 903 {
				t.Fatalf("unexpected lookup target %s/%d", table, id)
			}
			return false, nil
		},
	)
	if err != nil || applyCalls != 2 || len(publisher.messages) != 1 {
		t.Fatalf("err=%v applies=%d DLQ=%d", err, applyCalls, len(publisher.messages))
	}
	dlq, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if dlq.Failure.Class != string(kafkaFailurePermanent) || dlq.Failure.Code != kafkaFailureCodeDatabaseForeignKey || dlq.Source.Offset != message.Offset {
		t.Fatalf("DLQ=%+v", dlq)
	}
}

func TestKafkaForeignKeyParentLookupErrorAndCancellationRemainRetryable(t *testing.T) {
	message := kafka.Message{Topic: "test", Partition: 1, Offset: 2, Value: []byte("raw")}
	fkErr := testPostgresForeignKeyError("fk_post_behaviors_post")
	publisher := &fakeRawKafkaMessagePublisher{}
	lookupErr := errors.New("database connection refused")
	err := publishConfirmedMissingParentKafkaForeignKeyDLQ(context.Background(), nil,
		func(context.Context, *gorm.DB, string, uint) (bool, error) { return false, lookupErr },
		publisher, "dlq", kafkaConsumerUserBehaviorProjection, message, fkErr, 1, 22)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseUnavailable || len(publisher.messages) != 0 {
		t.Fatalf("lookup failure class=%q code=%q DLQ=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), len(publisher.messages), err)
	}
	err = publishConfirmedMissingParentKafkaForeignKeyDLQ(context.Background(), nil,
		func(context.Context, *gorm.DB, string, uint) (bool, error) { return true, nil },
		publisher, "dlq", kafkaConsumerUserBehaviorProjection, message, fkErr, 1, 22)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction || len(publisher.messages) != 0 {
		t.Fatalf("present parent class=%q code=%q DLQ=%d err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), len(publisher.messages), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = publishConfirmedMissingParentKafkaForeignKeyDLQ(ctx, nil, nil, publisher, "dlq", kafkaConsumerUserBehaviorProjection, message, fkErr, 1, 22)
	if !errors.Is(err, context.Canceled) || len(publisher.messages) != 0 {
		t.Fatalf("cancelled lookup err=%v DLQ=%d", err, len(publisher.messages))
	}
}
