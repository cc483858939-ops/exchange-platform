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

// KafkaCommittedLag includes every partition and messages already prefetched by
// readers but not committed. It performs no work on the consumer commit path.
func KafkaCommittedLag(ctx context.Context, cfg config.KafkaConfig, topic, group string) (int64, error) {
	brokers := normalizedBrokers(cfg.Brokers)
	if len(brokers) == 0 || strings.TrimSpace(topic) == "" || strings.TrimSpace(group) == "" {
		return 0, errors.New("Kafka lag configuration is incomplete")
	}
	client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
	return sampleKafkaCommittedLag(ctx, client, topic, group)
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
