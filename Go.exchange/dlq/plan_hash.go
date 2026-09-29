package dlq

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"sort"
	"strings"
	"time"

	"Go.exchange/eventing"
)

const BatchReplayPlanVersion = 1

// BatchReplaySelectors contains the normalized inputs that select a batch.
// Limit and scan limit are included because they affect the candidate set and
// the completeness guarantee presented to the operator.
type BatchReplaySelectors struct {
	Consumer     string
	SourceTopic  string
	ErrorClass   string
	ErrorCode    string
	EventID      string
	FailedAfter  string
	FailedBefore string
	Limit        int
	ScanLimit    int
}

func NormalizeBatchReplaySelectors(filter Filter) BatchReplaySelectors {
	selectors := BatchReplaySelectors{
		Consumer: strings.TrimSpace(filter.Consumer), SourceTopic: strings.TrimSpace(filter.SourceTopic),
		ErrorClass: strings.TrimSpace(filter.ErrorClass), ErrorCode: strings.TrimSpace(filter.ErrorCode),
		EventID: strings.TrimSpace(filter.EventID), Limit: filter.Limit, ScanLimit: filter.ScanLimit,
	}
	if filter.FailedAfter != nil {
		selectors.FailedAfter = filter.FailedAfter.UTC().Format(time.RFC3339Nano)
	}
	if filter.FailedBefore != nil {
		selectors.FailedBefore = filter.FailedBefore.UTC().Format(time.RFC3339Nano)
	}
	return selectors
}

// BuildBatchReplayPlanHash hashes a length-framed canonical representation,
// never a map-encoded JSON object. Record order is normalized to DLQ partition
// and offset ascending before hashing.
func BuildBatchReplayPlanHash(dlqTopic string, selectors BatchReplaySelectors, records []LocatedRecord) (string, error) {
	dlqTopic = strings.TrimSpace(dlqTopic)
	if dlqTopic == "" {
		return "", errors.New("DLQ topic is required for a batch replay plan")
	}
	ordered := append([]LocatedRecord(nil), records...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].DLQPartition != ordered[j].DLQPartition {
			return ordered[i].DLQPartition < ordered[j].DLQPartition
		}
		return ordered[i].DLQOffset < ordered[j].DLQOffset
	})

	digest := sha256.New()
	writeString(digest, "goexchange.kafka-dlq.batch-replay-plan")
	writeInt(digest, BatchReplayPlanVersion)
	writeString(digest, dlqTopic)
	writeString(digest, strings.TrimSpace(selectors.Consumer))
	writeString(digest, strings.TrimSpace(selectors.SourceTopic))
	writeString(digest, strings.TrimSpace(selectors.ErrorClass))
	writeString(digest, strings.TrimSpace(selectors.ErrorCode))
	writeString(digest, strings.TrimSpace(selectors.EventID))
	writeString(digest, normalizePlanTime(selectors.FailedAfter))
	writeString(digest, normalizePlanTime(selectors.FailedBefore))
	writeInt(digest, selectors.Limit)
	writeInt(digest, selectors.ScanLimit)
	writeInt(digest, len(ordered))

	for _, located := range ordered {
		if located.DLQTopic != dlqTopic || located.DLQPartition < 0 || located.DLQOffset < 0 {
			return "", errors.New("batch replay record has an invalid DLQ location")
		}
		record := located.Record
		writeInt(digest, located.DLQPartition)
		writeInt64(digest, located.DLQOffset)
		writeString(digest, record.Consumer)
		writeString(digest, record.Source.Topic)
		writeInt(digest, record.Source.Partition)
		writeInt64(digest, record.Source.Offset)
		writeString(digest, record.EventID)
		writeString(digest, record.Failure.Code)
		writeString(digest, record.Failure.FailedAt.UTC().Format(time.RFC3339Nano))
		sourceDigest := sourceMessageDigest(record.Source.Key, record.Source.Value, record.Source.Headers)
		writeBytes(digest, sourceDigest[:])
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func sourceMessageDigest(key, value []byte, headers []eventing.DeadLetterHeader) [sha256.Size]byte {
	message := sha256.New()
	writeByteSlice(message, key)
	writeByteSlice(message, value)
	writeInt(message, len(headers))
	for _, header := range headers {
		writeString(message, header.Key)
		writeByteSlice(message, header.Value)
	}
	var result [sha256.Size]byte
	copy(result[:], message.Sum(nil))
	return result
}

func normalizePlanTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func writeString(writer hash.Hash, value string) { writeBytes(writer, []byte(value)) }

func writeBytes(writer hash.Hash, value []byte) {
	writeInt64(writer, int64(len(value)))
	_, _ = writer.Write(value)
}

func writeByteSlice(writer hash.Hash, value []byte) {
	if value == nil {
		_, _ = writer.Write([]byte{0})
		return
	}
	_, _ = writer.Write([]byte{1})
	writeBytes(writer, value)
}

func writeInt(writer hash.Hash, value int) { writeInt64(writer, int64(value)) }

func writeInt64(writer hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}
