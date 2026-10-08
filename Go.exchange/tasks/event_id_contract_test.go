package tasks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

func TestKafkaConsumersRejectOverlongEventIDBeforeProjection(t *testing.T) {
	message := mustUserBehaviorRecoveryMessage(t, 100, strings.Repeat("a", 129), eventing.EventTypePostViewed, 7, 42, 0)
	for _, tc := range []struct {
		name   string
		decode func(kafka.Message) error
	}{
		{"behavior", func(m kafka.Message) error { _, err := decodeUserBehaviorMessage(m); return err }},
		{"snapshot", func(m kafka.Message) error { _, _, err := decodeLikeSnapshotMessage(m); return err }},
		{"notification", func(m kafka.Message) error { _, err := decodeNotificationActivity(m); return err }},
		{"recommendation", func(m kafka.Message) error { _, err := decodeRecommendationMetricEvent(m); return err }},
		{"embedding", func(m kafka.Message) error { _, err := decodePostEmbeddingMessage(m); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode(message)
			if kafkaFailureClassOf(err) != kafkaFailurePermanent || kafkaFailureCode(err) != kafkaFailureCodeDecodeEnvelope {
				t.Fatalf("overlong ID class=%s code=%s err=%v", kafkaFailureClassOf(err), kafkaFailureCode(err), err)
			}
		})
	}
}

func TestUserBehaviorOverlongEventIDDLQAndFollowingCanonicalProjection(t *testing.T) {
	bad := mustUserBehaviorRecoveryMessage(t, 100, strings.Repeat("a", 129), eventing.EventTypePostViewed, 7, 42, 0)
	padded := mustUserBehaviorRecoveryMessage(t, 101, " \tview-next\n", eventing.EventTypePostViewed, 7, 42, 0)
	canonical := mustUserBehaviorRecoveryMessage(t, 102, "view-next", eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad, padded, canonical}}
	publisher := &fakeRawKafkaMessagePublisher{}
	applied := 0
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
		applied++
		if len(records) != 1 || records[0].Envelope.ID != "view-next" {
			t.Fatalf("canonical same-batch deduplication failed: %+v", records)
		}
		return nil
	})
	if !errors.Is(err, io.EOF) || applied != 1 || len(reader.commits) != 1 || len(reader.commits[0]) != 3 || len(publisher.messages) != 1 {
		t.Fatalf("err=%v apply=%d commits=%v DLQ=%d", err, applied, reader.commits, len(publisher.messages))
	}
	record, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if record.Failure.Code != kafkaFailureCodeDecodeEnvelope || !bytes.Equal(record.Source.Value, bad.Value) {
		t.Fatalf("DLQ lost original ID/message: %+v", record)
	}
}

func TestUserBehaviorOverlongEventIDDLQFailureLeavesOffsetUncommitted(t *testing.T) {
	bad := mustUserBehaviorRecoveryMessage(t, 100, strings.Repeat("a", 129), eventing.EventTypePostViewed, 7, 42, 0)
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{bad}}
	publisher := &fakeRawKafkaMessagePublisher{err: errors.New("DLQ unavailable")}
	applied := false
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, publisher, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		applied = true
		return nil
	})
	if applied || len(reader.commits) != 0 || kafkaFailureCode(err) != kafkaFailureCodeDLQPublish {
		t.Fatalf("err=%v applied=%t commits=%v", err, applied, reader.commits)
	}
}
