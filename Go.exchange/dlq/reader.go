package dlq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
)

const (
	DefaultLimit          = 100
	DefaultScanLimit      = 10000
	MaxLimit              = 500
	MaxScanLimit          = 100000
	kafkaOperationTimeout = 15 * time.Second
)

type LocatedRecord struct {
	DLQTopic     string
	DLQPartition int
	DLQOffset    int64
	KafkaTime    time.Time
	Record       eventing.DeadLetterRecord
}

type Filter struct {
	Consumer     string
	SourceTopic  string
	ErrorClass   string
	ErrorCode    string
	EventID      string
	FailedAfter  *time.Time
	FailedBefore *time.Time
	Partition    *int
	Offset       *int64
	Limit        int
	ScanLimit    int
}

type ScanResult struct {
	Scanned   int  `json:"scanned"`
	Matched   int  `json:"matched"`
	Returned  int  `json:"returned"`
	Truncated bool `json:"truncated"`
}

type Reader interface {
	ReadAt(ctx context.Context, partition int, offset int64) (LocatedRecord, error)
	Scan(ctx context.Context, filter Filter) ([]LocatedRecord, ScanResult, error)
}

type KafkaDLQReader struct {
	brokers []string
	topic   string
}

func NewKafkaDLQReader(kafkaConfig config.KafkaConfig) (*KafkaDLQReader, error) {
	brokers := make([]string, 0, len(kafkaConfig.Brokers))
	for _, broker := range kafkaConfig.Brokers {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	topic := strings.TrimSpace(kafkaConfig.ConsumerDLQTopic)
	if topic == "" {
		return nil, errors.New("consumer DLQ topic is required")
	}
	return &KafkaDLQReader{brokers: brokers, topic: topic}, nil
}

func (r *KafkaDLQReader) ReadAt(ctx context.Context, partition int, offset int64) (LocatedRecord, error) {
	if r == nil {
		return LocatedRecord{}, errors.New("Kafka DLQ reader is nil")
	}
	if ctx == nil {
		return LocatedRecord{}, errors.New("Kafka DLQ read context is nil")
	}
	if partition < 0 || offset < 0 {
		return LocatedRecord{}, errors.New("DLQ partition and offset must be non-negative")
	}
	first, last, _, err := r.partitionBounds(ctx, partition, nil)
	if err != nil {
		return LocatedRecord{}, err
	}
	if offset < first || offset >= last {
		return LocatedRecord{}, fmt.Errorf("DLQ record not found at partition=%d offset=%d", partition, offset)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: r.brokers, Topic: r.topic, Partition: partition,
		MinBytes: 1, MaxBytes: 10e6, StartOffset: offset,
	})
	defer reader.Close()
	if err := reader.SetOffset(offset); err != nil {
		return LocatedRecord{}, fmt.Errorf("seek DLQ partition=%d offset=%d: %w", partition, offset, err)
	}
	fetchCtx, cancel := context.WithTimeout(ctx, kafkaOperationTimeout)
	message, err := reader.FetchMessage(fetchCtx)
	cancel()
	if err != nil {
		return LocatedRecord{}, fmt.Errorf("read DLQ partition=%d offset=%d: %w", partition, offset, err)
	}
	if message.Offset != offset {
		return LocatedRecord{}, fmt.Errorf("DLQ record not found at partition=%d offset=%d", partition, offset)
	}
	return locatedDeadLetter(message)
}

func (r *KafkaDLQReader) Scan(ctx context.Context, filter Filter) ([]LocatedRecord, ScanResult, error) {
	if r == nil {
		return nil, ScanResult{}, errors.New("Kafka DLQ reader is nil")
	}
	if ctx == nil {
		return nil, ScanResult{}, errors.New("Kafka DLQ scan context is nil")
	}
	limit, scanLimit, err := normalizeScanLimits(filter.Limit, filter.ScanLimit)
	if err != nil {
		return nil, ScanResult{}, err
	}
	if filter.Offset != nil && filter.Partition == nil {
		return nil, ScanResult{}, errors.New("--offset requires --partition")
	}
	if filter.Offset != nil {
		located, err := r.ReadAt(ctx, *filter.Partition, *filter.Offset)
		if err != nil {
			return nil, ScanResult{}, err
		}
		if filter.matches(located.Record) {
			return []LocatedRecord{located}, ScanResult{Scanned: 1, Matched: 1, Returned: 1}, nil
		}
		return nil, ScanResult{Scanned: 1}, nil
	}
	partitions, err := r.partitions(ctx)
	if err != nil {
		return nil, ScanResult{}, err
	}
	if filter.Partition != nil {
		partitions = filterPartitionIDs(partitions, *filter.Partition)
		if len(partitions) == 0 {
			return nil, ScanResult{}, fmt.Errorf("DLQ partition %d does not exist", *filter.Partition)
		}
	}
	ranges := make([]scanRange, 0, len(partitions))
	for _, partition := range partitions {
		first, last, start, err := r.partitionBounds(ctx, partition, filter.FailedAfter)
		if err != nil {
			return nil, ScanResult{}, err
		}
		if start < first {
			start = first
		}
		if start > last {
			start = last
		}
		ranges = append(ranges, scanRange{partition: partition, first: first, last: last, next: start})
	}

	var result ScanResult
	locatedRecords := make([]LocatedRecord, 0)
	for index := range ranges {
		rangeInfo := &ranges[index]
		if rangeInfo.next >= rangeInfo.last {
			continue
		}
		reader := kafka.NewReader(kafka.ReaderConfig{
			Brokers: r.brokers, Topic: r.topic, Partition: rangeInfo.partition,
			MinBytes: 1, MaxBytes: 10e6, StartOffset: rangeInfo.next,
		})
		if err := reader.SetOffset(rangeInfo.next); err != nil {
			_ = reader.Close()
			return nil, result, fmt.Errorf("seek DLQ partition=%d offset=%d: %w", rangeInfo.partition, rangeInfo.next, err)
		}
		for rangeInfo.next < rangeInfo.last && result.Scanned < scanLimit {
			fetchCtx, cancel := context.WithTimeout(ctx, kafkaOperationTimeout)
			message, fetchErr := reader.FetchMessage(fetchCtx)
			cancel()
			if fetchErr != nil {
				_ = reader.Close()
				if ctx.Err() != nil {
					return nil, result, ctx.Err()
				}
				return nil, result, fmt.Errorf("scan DLQ partition=%d: %w", rangeInfo.partition, fetchErr)
			}
			if message.Offset >= rangeInfo.last {
				rangeInfo.next = rangeInfo.last
				break
			}
			result.Scanned++
			rangeInfo.next = message.Offset + 1
			located, decodeErr := locatedDeadLetter(message)
			if decodeErr != nil {
				_ = reader.Close()
				return nil, result, decodeErr
			}
			if filter.matches(located.Record) {
				result.Matched++
				locatedRecords = append(locatedRecords, located)
			}
		}
		_ = reader.Close()
		if result.Scanned >= scanLimit {
			break
		}
	}
	result.Truncated = scanLimitHasRemaining(result.Scanned, scanLimit, ranges)
	sort.Slice(locatedRecords, func(i, j int) bool {
		left, right := locatedRecords[i], locatedRecords[j]
		if !left.Record.Failure.FailedAt.Equal(right.Record.Failure.FailedAt) {
			return left.Record.Failure.FailedAt.After(right.Record.Failure.FailedAt)
		}
		if left.DLQPartition != right.DLQPartition {
			return left.DLQPartition < right.DLQPartition
		}
		return left.DLQOffset > right.DLQOffset
	})
	if len(locatedRecords) > limit {
		locatedRecords = locatedRecords[:limit]
	}
	result.Returned = len(locatedRecords)
	return locatedRecords, result, nil
}

type scanRange struct {
	partition int
	first     int64
	last      int64
	next      int64
}

func (r *KafkaDLQReader) partitionBounds(ctx context.Context, partition int, timestamp *time.Time) (first, last, start int64, err error) {
	operationCtx, cancel := context.WithTimeout(ctx, kafkaOperationTimeout)
	defer cancel()
	var dialErrors []error
	for _, broker := range r.brokers {
		conn, dialErr := kafka.DialLeader(operationCtx, "tcp", broker, r.topic, partition)
		if dialErr != nil {
			dialErrors = append(dialErrors, fmt.Errorf("%s: %w", broker, dialErr))
			continue
		}
		if deadline, ok := operationCtx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		first, last, err = conn.ReadOffsets()
		if err == nil {
			start = first
			if timestamp != nil {
				start, err = conn.ReadOffset(*timestamp)
				if err == nil && start < 0 {
					start = last
				}
			}
		}
		_ = conn.Close()
		if err == nil {
			return first, last, start, nil
		}
		dialErrors = append(dialErrors, err)
	}
	return 0, 0, 0, fmt.Errorf("read Kafka DLQ partition offsets: %w", errors.Join(dialErrors...))
}

func (r *KafkaDLQReader) partitions(ctx context.Context) ([]int, error) {
	operationCtx, cancel := context.WithTimeout(ctx, kafkaOperationTimeout)
	defer cancel()
	var dialErrors []error
	for _, broker := range r.brokers {
		conn, err := kafka.DialContext(operationCtx, "tcp", broker)
		if err != nil {
			dialErrors = append(dialErrors, fmt.Errorf("%s: %w", broker, err))
			continue
		}
		if deadline, ok := operationCtx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		partitions, readErr := conn.ReadPartitions(r.topic)
		_ = conn.Close()
		if readErr == nil {
			ids := make([]int, 0, len(partitions))
			for _, partition := range partitions {
				if partition.Error != nil {
					return nil, partition.Error
				}
				ids = append(ids, partition.ID)
			}
			sort.Ints(ids)
			return ids, nil
		}
		dialErrors = append(dialErrors, readErr)
	}
	return nil, fmt.Errorf("read Kafka DLQ partitions: %w", errors.Join(dialErrors...))
}

func scanLimitHasRemaining(scanned, scanLimit int, ranges []scanRange) bool {
	if scanned < scanLimit {
		return false
	}
	for _, rangeInfo := range ranges {
		if rangeInfo.next < rangeInfo.last {
			return true
		}
	}
	return false
}

func filterPartitionIDs(partitions []int, requested int) []int {
	for _, partition := range partitions {
		if partition == requested {
			return []int{requested}
		}
	}
	return nil
}

func locatedDeadLetter(message kafka.Message) (LocatedRecord, error) {
	record, err := eventing.DecodeDeadLetterRecord(message.Value)
	if err != nil {
		return LocatedRecord{}, fmt.Errorf("decode DLQ record at partition=%d offset=%d: %w", message.Partition, message.Offset, err)
	}
	return LocatedRecord{
		DLQTopic: message.Topic, DLQPartition: message.Partition, DLQOffset: message.Offset,
		KafkaTime: message.Time.UTC(), Record: record,
	}, nil
}

func (f Filter) matches(record eventing.DeadLetterRecord) bool {
	return (f.Consumer == "" || record.Consumer == f.Consumer) &&
		(f.SourceTopic == "" || record.Source.Topic == f.SourceTopic) &&
		(f.ErrorClass == "" || record.Failure.Class == f.ErrorClass) &&
		(f.ErrorCode == "" || record.Failure.Code == f.ErrorCode) &&
		(f.EventID == "" || record.EventID == f.EventID) &&
		(f.FailedAfter == nil || !record.Failure.FailedAt.Before(*f.FailedAfter)) &&
		(f.FailedBefore == nil || !record.Failure.FailedAt.After(*f.FailedBefore))
}

func normalizeScanLimits(limit, scanLimit int) (int, int, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if scanLimit == 0 {
		scanLimit = DefaultScanLimit
	}
	if limit < 1 || limit > MaxLimit {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d", MaxLimit)
	}
	if scanLimit < 1 || scanLimit > MaxScanLimit {
		return 0, 0, fmt.Errorf("scan-limit must be between 1 and %d", MaxScanLimit)
	}
	return limit, scanLimit, nil
}
