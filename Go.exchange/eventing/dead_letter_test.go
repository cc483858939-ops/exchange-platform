package eventing

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

func validDeadLetterRecord() DeadLetterRecord {
	return DeadLetterRecord{
		SchemaVersion: DeadLetterSchemaVersion,
		Consumer:      "like_snapshot_projection",
		EventID:       "event-1",
		Source: DeadLetterSource{
			Topic: "goexchange.post.like.snapshot.v1", Partition: 2, Offset: 7,
			Key: []byte{0, 0xff}, Value: []byte{0x80, 1},
			Headers: []DeadLetterHeader{{Key: "trace", Value: []byte{0, 0xfe}}},
			Time:    time.Date(2026, 9, 28, 1, 2, 3, 4, time.UTC),
		},
		Failure: DeadLetterFailure{
			Class: "permanent", Code: "decode_envelope", Reason: "invalid envelope", Attempts: 1,
			FailedAt: time.Date(2026, 9, 28, 1, 2, 4, 5, time.UTC),
		},
	}
}

func TestDeadLetterRecordEncodeDecodePreservesBinaryBytes(t *testing.T) {
	original := validDeadLetterRecord()
	encoded, err := EncodeDeadLetterRecord(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDeadLetterRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != DeadLetterSchemaVersion || decoded.Consumer != original.Consumer || decoded.EventID != original.EventID {
		t.Fatalf("record metadata changed: %#v", decoded)
	}
	if decoded.Source.Topic != original.Source.Topic || decoded.Source.Partition != original.Source.Partition || decoded.Source.Offset != original.Source.Offset || !decoded.Source.Time.Equal(original.Source.Time) {
		t.Fatalf("source metadata changed: %#v", decoded.Source)
	}
	if !bytes.Equal(decoded.Source.Key, original.Source.Key) || !bytes.Equal(decoded.Source.Value, original.Source.Value) ||
		len(decoded.Source.Headers) != 1 || decoded.Source.Headers[0].Key != original.Source.Headers[0].Key || !bytes.Equal(decoded.Source.Headers[0].Value, original.Source.Headers[0].Value) {
		t.Fatalf("source bytes changed: %#v", decoded.Source)
	}
	if decoded.Failure != original.Failure {
		t.Fatalf("failure metadata changed: %#v want %#v", decoded.Failure, original.Failure)
	}
}

func TestDeadLetterRecordPreservesNilAndEmptyByteSlices(t *testing.T) {
	record := validDeadLetterRecord()
	record.Source.Key = []byte{}
	record.Source.Value = nil
	record.Source.Headers = []DeadLetterHeader{{Key: "empty", Value: []byte{}}, {Key: "nil", Value: nil}}
	encoded, err := EncodeDeadLetterRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDeadLetterRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Source.Key == nil || len(decoded.Source.Key) != 0 || decoded.Source.Value != nil {
		t.Fatalf("key/value nil-vs-empty changed: key=%#v value=%#v", decoded.Source.Key, decoded.Source.Value)
	}
	if len(decoded.Source.Headers) != 2 || decoded.Source.Headers[0].Value == nil || decoded.Source.Headers[1].Value != nil {
		t.Fatalf("header nil-vs-empty changed: %#v", decoded.Source.Headers)
	}
}

func TestDeadLetterRecordValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DeadLetterRecord)
		want string
	}{
		{name: "unknown schema", edit: func(record *DeadLetterRecord) { record.SchemaVersion++ }, want: "unsupported dead letter schema version"},
		{name: "missing consumer", edit: func(record *DeadLetterRecord) { record.Consumer = " " }, want: "consumer is required"},
		{name: "missing source topic", edit: func(record *DeadLetterRecord) { record.Source.Topic = "" }, want: "source topic is required"},
		{name: "negative partition", edit: func(record *DeadLetterRecord) { record.Source.Partition = -1 }, want: "partition must be non-negative"},
		{name: "negative offset", edit: func(record *DeadLetterRecord) { record.Source.Offset = -1 }, want: "offset must be non-negative"},
		{name: "missing failure class", edit: func(record *DeadLetterRecord) { record.Failure.Class = "" }, want: "failure class is required"},
		{name: "missing failure code", edit: func(record *DeadLetterRecord) { record.Failure.Code = "" }, want: "failure code is required"},
		{name: "attempts zero", edit: func(record *DeadLetterRecord) { record.Failure.Attempts = 0 }, want: "attempts must be at least one"},
		{name: "missing failed at", edit: func(record *DeadLetterRecord) { record.Failure.FailedAt = time.Time{} }, want: "failed_at is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validDeadLetterRecord()
			test.edit(&record)
			if err := ValidateDeadLetterRecord(record); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeDeadLetterRecordRejectsMalformedJSONAndUnknownVersion(t *testing.T) {
	if _, err := DecodeDeadLetterRecord([]byte("{")); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	raw, err := json.Marshal(validDeadLetterRecord())
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["schema_version"] = float64(99)
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeDeadLetterRecord(raw); err == nil || !strings.Contains(err.Error(), "unsupported dead letter schema version") {
		t.Fatalf("unknown schema error=%v", err)
	}
}

func TestDeadLetterReasonIsCappedWithoutTruncatingSourceBytes(t *testing.T) {
	source := kafka.Message{Topic: "source", Key: []byte{0xfe}, Value: bytes.Repeat([]byte{0xff}, MaxDeadLetterReasonBytes+10)}
	record, err := NewDeadLetterRecord("consumer", "", source, DeadLetterFailure{
		Class: "permanent", Code: "decode_payload", Reason: strings.Repeat("x", MaxDeadLetterReasonBytes+200), Attempts: 1, FailedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Failure.Reason) > MaxDeadLetterReasonBytes {
		t.Fatalf("reason bytes=%d max=%d", len(record.Failure.Reason), MaxDeadLetterReasonBytes)
	}
	if !bytes.Equal(record.Source.Key, source.Key) || !bytes.Equal(record.Source.Value, source.Value) {
		t.Fatal("source bytes were truncated")
	}
}

func TestNewDeadLetterRecordAndBuildMessage(t *testing.T) {
	failedAt := time.Date(2026, 9, 28, 2, 3, 4, 0, time.FixedZone("plus-eight", 8*60*60))
	record, err := NewDeadLetterRecord(" consumer ", "event-42", kafka.Message{
		Topic: "source", Partition: 3, Offset: 99, Key: []byte("key"), Value: []byte("value"),
		Headers: []kafka.Header{{Key: "trace", Value: []byte("id")}}, Time: failedAt,
	}, DeadLetterFailure{Class: "permanent", Code: "invalid_payload", Attempts: 2, FailedAt: failedAt})
	if err != nil {
		t.Fatal(err)
	}
	message, err := BuildDeadLetterMessage(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(message.Key) != "consumer:source:3:99" || !message.Time.Equal(failedAt.UTC()) {
		t.Fatalf("DLQ key/time=%q/%s", message.Key, message.Time)
	}
	decoded, err := DecodeDeadLetterRecord(message.Value)
	if err != nil || decoded.Consumer != "consumer" || decoded.EventID != "event-42" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}
