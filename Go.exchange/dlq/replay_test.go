package dlq

import (
	"bytes"
	"testing"
	"time"

	"Go.exchange/eventing"
)

func TestBuildReplayMessagePreservesPayloadAndReplacesProvenance(t *testing.T) {
	located := LocatedRecord{
		DLQTopic: "consumer-dlq", DLQPartition: 4, DLQOffset: 108,
		Record: eventing.DeadLetterRecord{
			SchemaVersion: eventing.DeadLetterSchemaVersion,
			Consumer:      "user_behavior_projection",
			Source: eventing.DeadLetterSource{
				Topic: "behavior", Partition: 3, Offset: 77, Key: []byte{0, 0xff}, Value: []byte{0x80, 0x01},
				Headers: []eventing.DeadLetterHeader{
					{Key: "trace", Value: []byte{0xff, 0}},
					{Key: ReplayHeader, Value: []byte("old")},
					{Key: ReplayIDHeader, Value: []byte("old-id")},
					{Key: ReplayIDHeader, Value: []byte("older-id")},
					{Key: ReplayedAtHeader, Value: []byte("old-time")},
					{Key: ReplayDLQTopicHeader, Value: []byte("old-topic")},
					{Key: ReplayDLQPartitionHeader, Value: []byte("9")},
					{Key: ReplayDLQOffsetHeader, Value: []byte("99")},
				},
			},
			Failure: eventing.DeadLetterFailure{Class: "permanent", Code: "decode_envelope", Attempts: 1, FailedAt: time.Now().UTC()},
		},
	}
	replayedAt := time.Date(2026, 9, 28, 2, 3, 4, 567, time.FixedZone("plus-eight", 8*60*60))
	first, err := BuildReplayMessage(located, "replay-uuid", replayedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReplayMessage(LocatedRecord{
		DLQTopic: located.DLQTopic, DLQPartition: located.DLQPartition, DLQOffset: located.DLQOffset,
		Record: eventing.DeadLetterRecord{
			SchemaVersion: located.Record.SchemaVersion, Consumer: located.Record.Consumer,
			Source: eventing.DeadLetterSource{
				Topic: located.Record.Source.Topic, Partition: located.Record.Source.Partition, Offset: located.Record.Source.Offset,
				Key: located.Record.Source.Key, Value: located.Record.Source.Value,
				Headers: append(located.Record.Source.Headers, eventing.DeadLetterHeader{Key: ReplayIDHeader, Value: []byte("stale")}),
			},
			Failure: located.Record.Failure,
		},
	}, "replay-uuid", replayedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Key, located.Record.Source.Key) || !bytes.Equal(first.Value, located.Record.Source.Value) ||
		!bytes.Equal(second.Key, located.Record.Source.Key) || !bytes.Equal(second.Value, located.Record.Source.Value) {
		t.Fatal("source business key/value changed")
	}
	if first.Partition != 0 || first.Partition == located.Record.Source.Partition || !first.Time.Equal(replayedAt.UTC()) {
		t.Fatalf("replay partition/time=%d/%s; original partition=%d", first.Partition, first.Time, located.Record.Source.Partition)
	}
	if len(first.Headers) != 7 || len(second.Headers) != 7 {
		t.Fatalf("provenance headers accumulated or source headers lost: first=%#v second=%#v", first.Headers, second.Headers)
	}
	if first.Headers[0].Key != "trace" || !bytes.Equal(first.Headers[0].Value, []byte{0xff, 0}) {
		t.Fatalf("original header changed: %#v", first.Headers[0])
	}
	counts := make(map[string]int)
	for _, header := range second.Headers {
		counts[header.Key]++
	}
	for _, key := range []string{ReplayHeader, ReplayIDHeader, ReplayedAtHeader, ReplayDLQTopicHeader, ReplayDLQPartitionHeader, ReplayDLQOffsetHeader} {
		if counts[key] != 1 {
			t.Fatalf("header %q count=%d headers=%#v", key, counts[key], second.Headers)
		}
	}
	if string(first.Headers[2].Value) != "replay-uuid" || string(first.Headers[0].Value) == "replay-uuid" {
		t.Fatalf("replay id provenance missing: %#v", first.Headers)
	}
}

func TestReplayHeaderFilterPreservesOtherBusinessHeaders(t *testing.T) {
	located := LocatedRecord{DLQTopic: "consumer-dlq", DLQPartition: 1, DLQOffset: 5, Record: replayTestRecord("user_behavior_projection", "behavior")}
	located.Record.Source.Headers = []eventing.DeadLetterHeader{
		{Key: "X-GoExchange-Replay-ID", Value: []byte("stale")},
		{Key: "authorization", Value: []byte("secret")},
	}
	message, err := BuildReplayMessage(located, "id", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Headers) != 7 || message.Headers[0].Key != "authorization" {
		t.Fatalf("headers=%#v", message.Headers)
	}
	if string(message.Headers[1].Value) != "true" || message.Headers[1].Key != ReplayHeader {
		t.Fatalf("replay marker missing: %#v", message.Headers)
	}
}
