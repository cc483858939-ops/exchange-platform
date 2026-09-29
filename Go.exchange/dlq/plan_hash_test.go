package dlq

import (
	"testing"
	"time"

	"Go.exchange/eventing"
)

func TestBuildBatchReplayPlanHashIsCanonicalAndBindsCandidateBytes(t *testing.T) {
	first := replayPlanHashRecord(2, 9)
	second := replayPlanHashRecord(1, 4)
	selectors := BatchReplaySelectors{
		Consumer: " user_behavior_projection ", ErrorCode: " decode_envelope ",
		FailedAfter: "2026-09-28T09:00:00+08:00", Limit: 100, ScanLimit: 10000,
	}
	canonicalSelectors := BatchReplaySelectors{
		Consumer: "user_behavior_projection", ErrorCode: "decode_envelope",
		FailedAfter: "2026-09-28T01:00:00Z", Limit: 100, ScanLimit: 10000,
	}
	firstHash, err := BuildBatchReplayPlanHash(" consumer-dlq ", selectors, []LocatedRecord{first, second})
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := BuildBatchReplayPlanHash("consumer-dlq", canonicalSelectors, []LocatedRecord{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical hash changed with order/normalization: %s != %s", firstHash, secondHash)
	}
	changedPayload := replayPlanHashRecord(2, 9)
	changedPayload.Record.Source.Value = []byte("changed payload")
	changedHash, err := BuildBatchReplayPlanHash("consumer-dlq", canonicalSelectors, []LocatedRecord{changedPayload, second})
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == firstHash {
		t.Fatal("source payload change did not change batch plan hash")
	}
	changedSetHash, err := BuildBatchReplayPlanHash("consumer-dlq", canonicalSelectors, []LocatedRecord{second})
	if err != nil {
		t.Fatal(err)
	}
	if changedSetHash == firstHash {
		t.Fatal("candidate set change did not change batch plan hash")
	}
	changedSelectors := canonicalSelectors
	changedSelectors.ErrorCode = "database_transaction"
	selectorHash, err := BuildBatchReplayPlanHash("consumer-dlq", changedSelectors, []LocatedRecord{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if selectorHash == firstHash {
		t.Fatal("selector change did not change batch plan hash")
	}
}

func TestSourceMessageDigestBindsHeaderOrderAndNilVsEmpty(t *testing.T) {
	key, value := []byte("key"), []byte("value")
	headers := []eventing.DeadLetterHeader{{Key: "a", Value: []byte("1")}, {Key: "b", Value: []byte("2")}}
	original := sourceMessageDigest(key, value, headers)
	reordered := sourceMessageDigest(key, value, []eventing.DeadLetterHeader{headers[1], headers[0]})
	if original == reordered {
		t.Fatal("header order did not affect source digest")
	}
	if sourceMessageDigest(nil, value, nil) == sourceMessageDigest([]byte{}, value, nil) {
		t.Fatal("nil and empty source key were not distinguished")
	}
}

func replayPlanHashRecord(partition int, offset int64) LocatedRecord {
	return LocatedRecord{
		DLQTopic: "consumer-dlq", DLQPartition: partition, DLQOffset: offset,
		Record: eventing.DeadLetterRecord{
			Consumer: "user_behavior_projection", EventID: "event-1",
			Source: eventing.DeadLetterSource{
				Topic: "behavior", Partition: 3, Offset: 91, Key: []byte("key"), Value: []byte("payload"),
				Headers: []eventing.DeadLetterHeader{{Key: "trace", Value: []byte("trace-id")}},
			},
			Failure: eventing.DeadLetterFailure{Code: "decode_envelope", FailedAt: time.Date(2026, 9, 28, 1, 2, 3, 123, time.UTC)},
		},
	}
}
