package eventing

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
)

type lagSampleClient struct {
	metadata  *kafka.MetadataResponse
	committed *kafka.OffsetFetchResponse
	ends      *kafka.ListOffsetsResponse
	err       error
	requested []int
}

func (client *lagSampleClient) Metadata(ctx context.Context, _ *kafka.MetadataRequest) (*kafka.MetadataResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return client.metadata, client.err
}
func (client *lagSampleClient) OffsetFetch(_ context.Context, request *kafka.OffsetFetchRequest) (*kafka.OffsetFetchResponse, error) {
	client.requested = request.Topics["events"]
	return client.committed, client.err
}
func (client *lagSampleClient) ListOffsets(_ context.Context, _ *kafka.ListOffsetsRequest) (*kafka.ListOffsetsResponse, error) {
	return client.ends, client.err
}

func TestCommittedLagIncludesEveryPartitionAndUncommittedMessages(t *testing.T) {
	client := &lagSampleClient{
		metadata:  &kafka.MetadataResponse{Topics: []kafka.Topic{{Name: "events", Partitions: []kafka.Partition{{ID: 0}, {ID: 1}, {ID: 2}}}}},
		committed: &kafka.OffsetFetchResponse{Topics: map[string][]kafka.OffsetFetchPartition{"events": {{Partition: 0, CommittedOffset: 10}, {Partition: 1, CommittedOffset: -1}, {Partition: 2, CommittedOffset: 1}}}},
		ends:      &kafka.ListOffsetsResponse{Topics: map[string][]kafka.PartitionOffsets{"events": {{Partition: 0, FirstOffset: 0, LastOffset: 110}, {Partition: 1, FirstOffset: 5, LastOffset: 15}, {Partition: 2, FirstOffset: 4, LastOffset: 9}}}},
	}
	// Prefetching up to 110 does not change the committed offset of 10.
	lag, err := sampleKafkaCommittedLag(context.Background(), client, "events", "projection")
	if err != nil || lag != 115 || len(client.requested) != 3 {
		t.Fatalf("lag=%d partitions=%v err=%v", lag, client.requested, err)
	}
	client.committed.Topics["events"][0].CommittedOffset = 110
	lag, err = sampleKafkaCommittedLag(context.Background(), client, "events", "projection")
	if err != nil || lag != 15 {
		t.Fatalf("commit progress lag=%d err=%v", lag, err)
	}
}

func TestCommittedLagRejectsMissingErroredAndTruncatedSamples(t *testing.T) {
	for _, test := range []struct {
		name                string
		offset, first, last int64
		missing             bool
		partitionError      error
	}{
		{"missing", 10, 0, 20, true, nil},
		{"partition-error", 10, 0, 20, false, errors.New("rebalance")},
		{"truncated", 30, 0, 20, false, nil},
		{"invalid-end", 10, 20, 5, false, nil},
		{"invalid-committed", -2, 0, 20, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &lagSampleClient{
				metadata:  &kafka.MetadataResponse{Topics: []kafka.Topic{{Name: "events", Partitions: []kafka.Partition{{ID: 0}}}}},
				committed: &kafka.OffsetFetchResponse{Topics: map[string][]kafka.OffsetFetchPartition{"events": {{Partition: 0, CommittedOffset: test.offset, Error: test.partitionError}}}},
				ends:      &kafka.ListOffsetsResponse{Topics: map[string][]kafka.PartitionOffsets{"events": {{Partition: 0, FirstOffset: test.first, LastOffset: test.last}}}},
			}
			if test.missing {
				delete(client.ends.Topics, "events")
			}
			if _, err := sampleKafkaCommittedLag(context.Background(), client, "events", "projection"); err == nil {
				t.Fatal("invalid sample reported empty/valid lag")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sampleKafkaCommittedLag(ctx, &lagSampleClient{}, "events", "projection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
