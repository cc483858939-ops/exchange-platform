package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

func TestKafkaProcessErrorClassificationAndUnwrap(t *testing.T) {
	sentinel := errors.New("storage unavailable")
	tests := []struct {
		name  string
		wrap  func(error) error
		class kafkaFailureClass
		code  string
	}{
		{name: "permanent", wrap: func(err error) error { return permanentKafkaError(kafkaFailureCodeInvalidPayload, err) }, class: kafkaFailurePermanent, code: kafkaFailureCodeInvalidPayload},
		{name: "retryable", wrap: func(err error) error { return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err) }, class: kafkaFailureRetryable, code: kafkaFailureCodeDatabaseTransaction},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.wrap(sentinel)
			if kafkaFailureClassOf(got) != test.class {
				t.Fatalf("class=%q want=%q", kafkaFailureClassOf(got), test.class)
			}
			if kafkaFailureCode(got) != test.code {
				t.Fatalf("code=%q want=%q", kafkaFailureCode(got), test.code)
			}
			if !errors.Is(got, sentinel) {
				t.Fatalf("errors.Is(%v, sentinel) = false", got)
			}
		})
	}
}

func TestRetryKafkaOperationImmediateSuccess(t *testing.T) {
	calls := 0
	var retryAttempts []int
	err := retryKafkaOperation(context.Background(), kafkaRetryPolicy{MaxAttempts: 5}, func(attempt int, _ error) {
		retryAttempts = append(retryAttempts, attempt)
	}, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 || len(retryAttempts) != 0 {
		t.Fatalf("err=%v calls=%d retries=%v want one successful call and no retry callback", err, calls, retryAttempts)
	}
}

func TestRetryKafkaOperationRetriesThenSucceeds(t *testing.T) {
	calls := 0
	var retryAttempts []int
	err := retryKafkaOperation(context.Background(), kafkaRetryPolicy{MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}, func(attempt int, _ error) {
		retryAttempts = append(retryAttempts, attempt)
	}, func() error {
		calls++
		if calls < 3 {
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("temporary"))
		}
		return nil
	})
	if err != nil || calls != 3 || len(retryAttempts) != 2 || retryAttempts[0] != 2 || retryAttempts[1] != 3 {
		t.Fatalf("err=%v calls=%d retry_attempts=%v want attempts 2 and 3", err, calls, retryAttempts)
	}
}

func TestRetryKafkaOperationStopsOnPermanentError(t *testing.T) {
	calls := 0
	retryCallbacks := 0
	sentinel := errors.New("invalid event")
	err := retryKafkaOperation(context.Background(), kafkaRetryPolicy{MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}, func(int, error) {
		retryCallbacks++
	}, func() error {
		calls++
		return permanentKafkaError(kafkaFailureCodeInvalidPayload, sentinel)
	})
	if !errors.Is(err, sentinel) || calls != 1 || retryCallbacks != 0 {
		t.Fatalf("err=%v calls=%d retry_callbacks=%d want one permanent attempt", err, calls, retryCallbacks)
	}
}

func TestRetryKafkaOperationExhaustsBoundedAttempts(t *testing.T) {
	calls := 0
	var retryAttempts []int
	sentinel := errors.New("database unavailable")
	err := retryKafkaOperation(context.Background(), kafkaRetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}, func(attempt int, _ error) {
		retryAttempts = append(retryAttempts, attempt)
	}, func() error {
		calls++
		return retryableKafkaError(kafkaFailureCodeDatabaseUnavailable, sentinel)
	})
	if !errors.Is(err, sentinel) || calls != 3 || len(retryAttempts) != 2 || retryAttempts[0] != 2 || retryAttempts[1] != 3 {
		t.Fatalf("err=%v calls=%d retry_attempts=%v want three calls and callbacks 2, 3", err, calls, retryAttempts)
	}
}

func TestRetryKafkaOperationStopsWhenContextIsCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var retryAttempts []int
	cancelTimer := time.AfterFunc(10*time.Millisecond, cancel)
	defer cancelTimer.Stop()
	err := retryKafkaOperation(ctx, kafkaRetryPolicy{MaxAttempts: 5, InitialBackoff: time.Hour, MaxBackoff: time.Hour}, func(attempt int, _ error) {
		retryAttempts = append(retryAttempts, attempt)
	}, func() error {
		calls++
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, errors.New("temporary"))
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || len(retryAttempts) != 0 {
		t.Fatalf("err=%v calls=%d retry_attempts=%v want cancellation without a nonexistent next attempt", err, calls, retryAttempts)
	}
}

func TestKafkaRetryBackoffMatchesPhaseOneSchedule(t *testing.T) {
	got := []time.Duration{
		kafkaRetryBackoff(defaultKafkaRetryPolicy, 1),
		kafkaRetryBackoff(defaultKafkaRetryPolicy, 2),
		kafkaRetryBackoff(defaultKafkaRetryPolicy, 3),
		kafkaRetryBackoff(defaultKafkaRetryPolicy, 4),
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("backoff[%d]=%s want=%s", index, got[index], want[index])
		}
	}
}

func TestBuildConsumerDLQMessagePreservesReplayData(t *testing.T) {
	sourceValue := []byte(`{"id":"evt-123","type":"post.like.snapshot"}`)
	source := kafka.Message{
		Topic: "snapshots", Partition: 2, Offset: 9912,
		Key: []byte{0xff, 0x00, 0x80}, Value: append([]byte(nil), sourceValue...),
		Time:    time.Date(2026, 9, 23, 1, 2, 3, 456, time.FixedZone("UTC+8", 8*60*60)),
		Headers: []kafka.Header{{Key: "trace", Value: []byte{0x00, 0x80, 0xff}}, {Key: "content-type", Value: []byte("application/json")}},
	}
	failedAt := time.Date(2026, 9, 23, 2, 3, 4, 567, time.FixedZone("UTC+8", 8*60*60))
	processErr := permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("snapshot values are invalid"))
	dlqMessage, err := buildConsumerDLQMessage(kafkaConsumerLikeSnapshotProjection, source, processErr, 1, failedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(dlqMessage.Key), "like_snapshot_projection:snapshots:2:9912"; got != want {
		t.Fatalf("DLQ key=%q want=%q", got, want)
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(dlqMessage.Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Consumer != kafkaConsumerLikeSnapshotProjection || payload.SourceTopic != source.Topic || payload.SourcePartition != source.Partition || payload.SourceOffset != source.Offset {
		t.Fatalf("source location metadata not preserved: %+v", payload)
	}
	if !bytes.Equal(payload.SourceKey, source.Key) || !bytes.Equal(payload.SourceValue, source.Value) {
		t.Fatalf("source bytes changed: key=%v value=%v", payload.SourceKey, payload.SourceValue)
	}
	if !payload.SourceTime.Equal(source.Time) || payload.EventID != "evt-123" {
		t.Fatalf("source time/event ID=%s/%q want=%s/evt-123", payload.SourceTime, payload.EventID, source.Time)
	}
	if len(payload.SourceHeaders) != len(source.Headers) {
		t.Fatalf("headers=%d want=%d", len(payload.SourceHeaders), len(source.Headers))
	}
	for index, header := range source.Headers {
		if payload.SourceHeaders[index].Key != header.Key || !bytes.Equal(payload.SourceHeaders[index].Value, header.Value) {
			t.Fatalf("header[%d]=%+v want=%+v", index, payload.SourceHeaders[index], header)
		}
	}
	if payload.ErrorClass != string(kafkaFailurePermanent) || payload.ErrorCode != kafkaFailureCodeInvalidPayload || payload.Reason != processErr.Error() || payload.Attempts != 1 || !payload.FailedAt.Equal(failedAt.UTC()) {
		t.Fatalf("failure metadata not preserved: %+v", payload)
	}
	if !bytes.Equal(source.Value, sourceValue) {
		t.Fatal("building the DLQ record mutated the source message")
	}
}

func TestBuildConsumerDLQMessageAllowsMalformedArbitraryBytes(t *testing.T) {
	value := append([]byte(`{"id":"`), 0xff)
	value = append(value, []byte(`"}`)...)
	message, err := buildConsumerDLQMessage("consumer", kafka.Message{Topic: "source", Key: []byte{0xfe}, Value: value}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, errors.New("invalid JSON")), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(message.Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EventID != "" || !bytes.Equal(payload.SourceKey, []byte{0xfe}) || !bytes.Equal(payload.SourceValue, value) {
		t.Fatalf("malformed source bytes were not preserved: %+v", payload)
	}
}

func TestBuildConsumerDLQMessagePreservesNilAndEmptyBytes(t *testing.T) {
	message, err := buildConsumerDLQMessage("consumer", kafka.Message{
		Topic: "source", Key: []byte{}, Value: nil,
		Headers: []kafka.Header{{Key: "empty", Value: []byte{}}, {Key: "nil", Value: nil}},
	}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, errors.New("invalid JSON")), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var payload consumerDLQPayload
	if err := json.Unmarshal(message.Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SourceKey == nil || len(payload.SourceKey) != 0 || payload.SourceValue != nil {
		t.Fatalf("source nil/empty bytes changed: key=%#v value=%#v", payload.SourceKey, payload.SourceValue)
	}
	if len(payload.SourceHeaders) != 2 || payload.SourceHeaders[0].Value == nil || payload.SourceHeaders[1].Value != nil {
		t.Fatalf("header nil/empty bytes changed: %#v", payload.SourceHeaders)
	}
}
