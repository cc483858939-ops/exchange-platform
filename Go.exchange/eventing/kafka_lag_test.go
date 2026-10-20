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

type groupDescriptionClient struct {
	response *kafka.DescribeGroupsResponse
	err      error
	request  *kafka.DescribeGroupsRequest
}

func (client *groupDescriptionClient) DescribeGroups(ctx context.Context, request *kafka.DescribeGroupsRequest) (*kafka.DescribeGroupsResponse, error) {
	client.request = request
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return client.response, client.err
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

func TestDescribeKafkaConsumerGroupReturnsStateMembersAndSubscriptions(t *testing.T) {
	client := &groupDescriptionClient{response: &kafka.DescribeGroupsResponse{Groups: []kafka.DescribeGroupsResponseGroup{{
		GroupID: "projection", GroupState: "Stable", Members: []kafka.DescribeGroupsResponseMember{{
			ClientID: "local-reader", MemberMetadata: kafka.DescribeGroupsResponseMemberMetadata{Topics: []string{"events", "other"}},
		}},
	}}}}
	description, err := describeKafkaConsumerGroup(context.Background(), client, " projection ")
	if err != nil {
		t.Fatal(err)
	}
	if description.GroupID != "projection" || description.State != "Stable" || len(description.Members) != 1 {
		t.Fatalf("unexpected group description: %+v", description)
	}
	if description.Members[0].ClientID != "local-reader" || len(description.Members[0].Topics) != 2 || description.Members[0].Topics[0] != "events" {
		t.Fatalf("member subscription missing: %+v", description.Members[0])
	}
	if client.request == nil || len(client.request.GroupIDs) != 1 || client.request.GroupIDs[0] != "projection" {
		t.Fatalf("unexpected DescribeGroups request: %+v", client.request)
	}
}

func TestDescribeKafkaConsumerGroupPreservesNonStableAndEmptyGroups(t *testing.T) {
	for _, test := range []struct {
		name  string
		state string
	}{
		{name: "empty", state: "Empty"},
		{name: "preparing rebalance", state: "PreparingRebalance"},
		{name: "completing rebalance", state: "CompletingRebalance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &groupDescriptionClient{response: &kafka.DescribeGroupsResponse{Groups: []kafka.DescribeGroupsResponseGroup{{GroupID: "projection", GroupState: test.state}}}}
			description, err := describeKafkaConsumerGroup(context.Background(), client, "projection")
			if err != nil || description.State != test.state || len(description.Members) != 0 {
				t.Fatalf("description=%+v err=%v", description, err)
			}
		})
	}
}

func TestDescribeKafkaConsumerGroupFailsClosedOnMissingGroupOrQueryFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *kafka.DescribeGroupsResponse
		err      error
	}{
		{name: "group missing", response: &kafka.DescribeGroupsResponse{}},
		{name: "group error", response: &kafka.DescribeGroupsResponse{Groups: []kafka.DescribeGroupsResponseGroup{{GroupID: "projection", Error: errors.New("group not found")}}}},
		{name: "query error", err: errors.New("broker unavailable")},
		{name: "missing response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &groupDescriptionClient{response: test.response, err: test.err}
			if _, err := describeKafkaConsumerGroup(context.Background(), client, "projection"); err == nil {
				t.Fatal("invalid group query was accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := describeKafkaConsumerGroup(ctx, &groupDescriptionClient{}, "projection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
