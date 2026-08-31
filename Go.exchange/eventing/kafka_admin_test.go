package eventing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/segmentio/kafka-go"
)

func validKafkaTopicConfig() config.KafkaConfig {
	return config.KafkaConfig{
		Brokers: []string{"kafka:9092"}, UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot", RecommendationEventsTopic: "recommendation", PostEmbeddingTopic: "embedding", ActivityEventsTopic: "activity", NotificationDLQTopic: "notification-dlq",
		TopicReplicationFactor: 1, UserBehaviorPartitions: 12, LikeSnapshotPartitions: 6, RecommendationEventsPartitions: 12, PostEmbeddingPartitions: 6, ActivityEventsPartitions: 12, NotificationDLQPartitions: 3,
	}
}

func TestRequiredKafkaTopics(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 6 {
		t.Fatalf("topics=%d want=6", len(specs))
	}
	want := []TopicSpec{
		{Name: "behavior", Partitions: 12, ReplicationFactor: 1},
		{Name: "snapshot", Partitions: 6, ReplicationFactor: 1},
		{Name: "recommendation", Partitions: 12, ReplicationFactor: 1},
		{Name: "embedding", Partitions: 6, ReplicationFactor: 1},
		{Name: "activity", Partitions: 12, ReplicationFactor: 1},
		{Name: "notification-dlq", Partitions: 3, ReplicationFactor: 1},
	}
	for index := range want {
		if specs[index] != want[index] {
			t.Fatalf("spec[%d]=%+v want=%+v", index, specs[index], want[index])
		}
	}
}

func TestRequiredKafkaTopicsRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		edit func(*config.KafkaConfig)
	}{
		{name: "empty brokers", edit: func(cfg *config.KafkaConfig) { cfg.Brokers = nil }},
		{name: "empty topic", edit: func(cfg *config.KafkaConfig) { cfg.UserBehaviorTopic = " " }},
		{name: "duplicate topic", edit: func(cfg *config.KafkaConfig) { cfg.UserBehaviorTopic = cfg.RecommendationEventsTopic }},
		{name: "invalid partitions", edit: func(cfg *config.KafkaConfig) { cfg.UserBehaviorPartitions = 0 }},
		{name: "invalid replication factor", edit: func(cfg *config.KafkaConfig) { cfg.TopicReplicationFactor = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validKafkaTopicConfig()
			test.edit(&cfg)
			if _, err := RequiredKafkaTopics(cfg); err == nil {
				t.Fatal("expected invalid Kafka topic configuration to fail")
			}
		})
	}
}

type fakeKafkaTopicAdmin struct {
	topics                   map[string][]kafka.Partition
	createCalls              int
	closeCalls               int
	createErr                error
	returnTopicAlreadyExists bool
	metadataUnavailableReads int
}

func (f *fakeKafkaTopicAdmin) CreateTopics(_ context.Context, topics ...kafka.TopicConfig) error {
	f.createCalls++
	if f.createErr != nil {
		return f.createErr
	}
	for _, topic := range topics {
		if _, exists := f.topics[topic.Topic]; exists {
			continue
		}
		partitions := make([]kafka.Partition, topic.NumPartitions)
		for index := range partitions {
			partitions[index] = kafka.Partition{
				Topic: topic.Topic, ID: index,
				Replicas: []kafka.Broker{{ID: 0}},
			}
		}
		f.topics[topic.Topic] = partitions
	}
	if f.returnTopicAlreadyExists {
		return kafka.TopicAlreadyExists
	}
	return nil
}

func (f *fakeKafkaTopicAdmin) ReadPartitions(_ context.Context, topics ...string) ([]kafka.Partition, error) {
	if len(topics) == 0 {
		var all []kafka.Partition
		for _, partitions := range f.topics {
			all = append(all, partitions...)
		}
		return all, nil
	}
	if f.metadataUnavailableReads > 0 {
		f.metadataUnavailableReads--
		return nil, nil
	}
	partitions, ok := f.topics[topics[0]]
	if !ok {
		return nil, kafka.UnknownTopicOrPartition
	}
	return partitions, nil
}

func (f *fakeKafkaTopicAdmin) Close() error {
	f.closeCalls++
	return nil
}

func testKafkaAdminOptions() kafkaProvisioningOptions {
	return kafkaProvisioningOptions{
		startupTimeout:       250 * time.Millisecond,
		retryInterval:        1 * time.Millisecond,
		metadataTimeout:      50 * time.Millisecond,
		metadataPollInterval: 1 * time.Millisecond,
	}
}

func runFakeKafkaProvisioning(t *testing.T, admin *fakeKafkaTopicAdmin, specs []TopicSpec) error {
	t.Helper()
	return ensureKafkaTopics(context.Background(), specs, func(context.Context) (kafkaTopicAdmin, error) {
		return admin, nil
	}, testKafkaAdminOptions())
}

func TestEnsureKafkaTopicsCreatesMissingTopicsAndIsIdempotent(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	admin := &fakeKafkaTopicAdmin{topics: make(map[string][]kafka.Partition)}
	if err := runFakeKafkaProvisioning(t, admin, specs); err != nil {
		t.Fatal(err)
	}
	if admin.createCalls != 1 || len(admin.topics) != len(specs) {
		t.Fatalf("create calls=%d topics=%d", admin.createCalls, len(admin.topics))
	}
	admin.createCalls = 0
	if err := runFakeKafkaProvisioning(t, admin, specs); err != nil {
		t.Fatal(err)
	}
	if admin.createCalls != 0 {
		t.Fatalf("idempotent run attempted %d creates", admin.createCalls)
	}
}

func TestEnsureKafkaTopicsHandlesExistingAndConcurrentCreate(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	admin := &fakeKafkaTopicAdmin{topics: map[string][]kafka.Partition{}}
	admin.topics[specs[0].Name] = fakePartitions(specs[0])
	admin.returnTopicAlreadyExists = true
	if err := runFakeKafkaProvisioning(t, admin, specs); err != nil {
		t.Fatal(err)
	}
	if admin.createCalls == 0 || len(admin.topics) != len(specs) {
		t.Fatalf("race create calls=%d topics=%d", admin.createCalls, len(admin.topics))
	}
}

func TestEnsureKafkaTopicsRejectsWrongTopology(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	admin := &fakeKafkaTopicAdmin{topics: map[string][]kafka.Partition{}}
	admin.topics[specs[0].Name] = fakePartitionsWithCount(specs[0], specs[0].Partitions-1)
	err = runFakeKafkaProvisioning(t, admin, specs)
	if err == nil || !strings.Contains(err.Error(), "unexpected topology") {
		t.Fatalf("error=%v want topology mismatch", err)
	}
}

func TestEnsureKafkaTopicsWaitsForMetadata(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	admin := &fakeKafkaTopicAdmin{topics: make(map[string][]kafka.Partition), metadataUnavailableReads: 2}
	if err := runFakeKafkaProvisioning(t, admin, specs); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureKafkaTopicsRetriesConnectionFailures(t *testing.T) {
	specs, err := RequiredKafkaTopics(validKafkaTopicConfig())
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = ensureKafkaTopics(context.Background(), specs, func(context.Context) (kafkaTopicAdmin, error) {
		attempts++
		return nil, errors.New("broker unavailable")
	}, testKafkaAdminOptions())
	if err == nil || attempts < 2 {
		t.Fatalf("error=%v attempts=%d want bounded retries", err, attempts)
	}
}

func fakePartitions(spec TopicSpec) []kafka.Partition {
	return fakePartitionsWithCount(spec, spec.Partitions)
}

func fakePartitionsWithCount(spec TopicSpec, count int) []kafka.Partition {
	partitions := make([]kafka.Partition, count)
	for index := range partitions {
		partitions[index] = kafka.Partition{
			Topic: spec.Name, ID: index,
			Replicas: []kafka.Broker{{ID: 0}},
		}
	}
	return partitions
}
