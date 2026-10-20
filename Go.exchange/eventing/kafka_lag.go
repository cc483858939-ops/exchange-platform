package eventing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/config"

	"github.com/segmentio/kafka-go"
)

type kafkaLagClient interface {
	Metadata(context.Context, *kafka.MetadataRequest) (*kafka.MetadataResponse, error)
	OffsetFetch(context.Context, *kafka.OffsetFetchRequest) (*kafka.OffsetFetchResponse, error)
	ListOffsets(context.Context, *kafka.ListOffsetsRequest) (*kafka.ListOffsetsResponse, error)
}

type kafkaDescribeGroupsClient interface {
	DescribeGroups(context.Context, *kafka.DescribeGroupsRequest) (*kafka.DescribeGroupsResponse, error)
}

type KafkaGroupMember struct {
	ClientID string
	Topics   []string
}

type KafkaGroupDescription struct {
	GroupID string
	State   string
	Members []KafkaGroupMember
}

// KafkaCommittedLag includes every partition and messages already prefetched by
// readers but not committed. It performs no work on the consumer commit path.
func KafkaCommittedLag(ctx context.Context, cfg config.KafkaConfig, topic, group string) (int64, error) {
	topic = strings.TrimSpace(topic)
	group = strings.TrimSpace(group)
	brokers := normalizedBrokers(cfg.Brokers)
	if len(brokers) == 0 || strings.TrimSpace(topic) == "" || strings.TrimSpace(group) == "" {
		return 0, errors.New("Kafka lag configuration is incomplete")
	}
	client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
	return sampleKafkaCommittedLag(ctx, client, topic, group)
}

// KafkaConsumerGroup returns the coordinator's current consumer-group state
// and each member's client ID and subscribed topics. It does not change group
// offsets or consume messages.
func KafkaConsumerGroup(ctx context.Context, cfg config.KafkaConfig, group string) (KafkaGroupDescription, error) {
	group = strings.TrimSpace(group)
	brokers := normalizedBrokers(cfg.Brokers)
	if len(brokers) == 0 || strings.TrimSpace(group) == "" {
		return KafkaGroupDescription{}, errors.New("Kafka consumer group configuration is incomplete")
	}
	client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
	return describeKafkaConsumerGroup(ctx, client, group)
}

func describeKafkaConsumerGroup(ctx context.Context, client kafkaDescribeGroupsClient, group string) (KafkaGroupDescription, error) {
	group = strings.TrimSpace(group)
	if ctx == nil {
		return KafkaGroupDescription{}, errors.New("Kafka consumer group context is nil")
	}
	if client == nil {
		return KafkaGroupDescription{}, errors.New("Kafka consumer group client is nil")
	}
	if group == "" {
		return KafkaGroupDescription{}, errors.New("Kafka consumer group id is required")
	}
	response, err := client.DescribeGroups(ctx, &kafka.DescribeGroupsRequest{GroupIDs: []string{group}})
	if err != nil {
		return KafkaGroupDescription{}, err
	}
	if response == nil {
		return KafkaGroupDescription{}, errors.New("Kafka consumer group response is missing")
	}
	for _, described := range response.Groups {
		if described.GroupID != group {
			continue
		}
		if described.Error != nil {
			return KafkaGroupDescription{}, described.Error
		}
		result := KafkaGroupDescription{GroupID: described.GroupID, State: described.GroupState}
		for _, member := range described.Members {
			result.Members = append(result.Members, KafkaGroupMember{
				ClientID: member.ClientID,
				Topics:   append([]string(nil), member.MemberMetadata.Topics...),
			})
		}
		return result, nil
	}
	if len(response.Groups) == 1 && response.Groups[0].Error != nil {
		return KafkaGroupDescription{}, response.Groups[0].Error
	}
	return KafkaGroupDescription{}, fmt.Errorf("Kafka consumer group %q was not returned", group)
}

func sampleKafkaCommittedLag(ctx context.Context, client kafkaLagClient, topic, group string) (int64, error) {
	metadata, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		return 0, err
	}
	partitions := make([]int, 0)
	if metadata != nil {
		for _, entry := range metadata.Topics {
			if entry.Name != topic {
				continue
			}
			if entry.Error != nil {
				return 0, entry.Error
			}
			for _, partition := range entry.Partitions {
				if partition.Error != nil {
					return 0, partition.Error
				}
				partitions = append(partitions, partition.ID)
			}
		}
	}
	if len(partitions) == 0 {
		return 0, errors.New("Kafka lag topic has no partitions")
	}
	committed, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: partitions}})
	if err != nil {
		return 0, err
	}
	if committed == nil {
		return 0, errors.New("Kafka lag offset response is missing")
	}
	if committed.Error != nil {
		return 0, committed.Error
	}
	requests := make([]kafka.OffsetRequest, 0, 2*len(partitions))
	for _, partition := range partitions {
		requests = append(requests, kafka.FirstOffsetOf(partition), kafka.LastOffsetOf(partition))
	}
	// Fetch ends after committed offsets to avoid a concurrent normal commit
	// looking ahead of an older end sample.
	ends, err := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: requests}})
	if err != nil {
		return 0, err
	}
	if ends == nil {
		return 0, errors.New("Kafka lag end response is missing")
	}
	offsets := make(map[int]kafka.OffsetFetchPartition)
	for _, partition := range committed.Topics[topic] {
		offsets[partition.Partition] = partition
	}
	boundaries := make(map[int]kafka.PartitionOffsets)
	for _, partition := range ends.Topics[topic] {
		boundaries[partition.Partition] = partition
	}
	var total int64
	for _, id := range partitions {
		offset, found := offsets[id]
		end, endFound := boundaries[id]
		if !found || !endFound || offset.Error != nil || end.Error != nil || offset.CommittedOffset < -1 || end.FirstOffset < 0 || end.LastOffset < end.FirstOffset {
			return 0, fmt.Errorf("Kafka lag partition %d sample is incomplete or invalid", id)
		}
		start := offset.CommittedOffset
		// Readers start at FirstOffset when a group has never committed or its
		// offset has expired below the retained log boundary.
		if start < end.FirstOffset {
			start = end.FirstOffset
		}
		if start > end.LastOffset {
			return 0, fmt.Errorf("Kafka lag partition %d committed offset exceeds log end", id)
		}
		total += end.LastOffset - start
	}
	return total, nil
}
