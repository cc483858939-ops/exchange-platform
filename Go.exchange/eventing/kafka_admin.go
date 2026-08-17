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

const (
	kafkaProvisioningStartupTimeout = 60 * time.Second
	kafkaProvisioningRetryInterval  = time.Second
	kafkaMetadataTimeout            = 30 * time.Second
	kafkaMetadataPollInterval       = 750 * time.Millisecond
)

// TopicSpec is the infrastructure contract for one required Kafka topic.
type TopicSpec struct {
	Name              string
	Partitions        int
	ReplicationFactor int
}

// RequiredKafkaTopics is the single source of the required Kafka topic set.
func RequiredKafkaTopics(cfg config.KafkaConfig) ([]TopicSpec, error) {
	if len(normalizedBrokers(cfg.Brokers)) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	if cfg.TopicReplicationFactor < 1 {
		return nil, errors.New("kafka topic replication factor must be at least 1")
	}

	specs := []TopicSpec{
		{Name: cfg.UserBehaviorTopic, Partitions: cfg.UserBehaviorPartitions, ReplicationFactor: cfg.TopicReplicationFactor},
		{Name: cfg.LikeSnapshotTopic, Partitions: cfg.LikeSnapshotPartitions, ReplicationFactor: cfg.TopicReplicationFactor},
		{Name: cfg.RecommendationEventsTopic, Partitions: cfg.RecommendationEventsPartitions, ReplicationFactor: cfg.TopicReplicationFactor},
	}
	seen := make(map[string]struct{}, len(specs))
	for index := range specs {
		specs[index].Name = strings.TrimSpace(specs[index].Name)
		if specs[index].Name == "" {
			return nil, fmt.Errorf("kafka topic %d has an empty name", index+1)
		}
		if specs[index].Partitions < 1 {
			return nil, fmt.Errorf("kafka topic %q must have at least one partition", specs[index].Name)
		}
		if _, exists := seen[specs[index].Name]; exists {
			return nil, fmt.Errorf("duplicate kafka topic name %q", specs[index].Name)
		}
		seen[specs[index].Name] = struct{}{}
	}
	return specs, nil
}

type kafkaTopicAdmin interface {
	CreateTopics(context.Context, ...kafka.TopicConfig) error
	ReadPartitions(context.Context, ...string) ([]kafka.Partition, error)
	Close() error
}

type kafkaTopicAdminFactory func(context.Context) (kafkaTopicAdmin, error)

type kafkaProvisioningOptions struct {
	startupTimeout       time.Duration
	retryInterval        time.Duration
	metadataTimeout      time.Duration
	metadataPollInterval time.Duration
}

func defaultKafkaProvisioningOptions() kafkaProvisioningOptions {
	return kafkaProvisioningOptions{
		startupTimeout:       kafkaProvisioningStartupTimeout,
		retryInterval:        kafkaProvisioningRetryInterval,
		metadataTimeout:      kafkaMetadataTimeout,
		metadataPollInterval: kafkaMetadataPollInterval,
	}
}

type kafkaConnTopicAdmin struct {
	conn *kafka.Conn
}

func (a *kafkaConnTopicAdmin) CreateTopics(ctx context.Context, topics ...kafka.TopicConfig) error {
	if err := a.setDeadline(ctx); err != nil {
		return err
	}
	return a.conn.CreateTopics(topics...)
}

func (a *kafkaConnTopicAdmin) ReadPartitions(ctx context.Context, topics ...string) ([]kafka.Partition, error) {
	if err := a.setDeadline(ctx); err != nil {
		return nil, err
	}
	return a.conn.ReadPartitions(topics...)
}

func (a *kafkaConnTopicAdmin) setDeadline(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return a.conn.SetDeadline(deadline)
	}
	return nil
}

func (a *kafkaConnTopicAdmin) Close() error {
	return a.conn.Close()
}

// EnsureKafkaTopics creates and verifies all required topics before producers
// or consumers are allowed to start. Existing topic topology is immutable here.
func EnsureKafkaTopics(ctx context.Context, cfg config.KafkaConfig) error {
	specs, err := RequiredKafkaTopics(cfg)
	if err != nil {
		return err
	}
	brokers := normalizedBrokers(cfg.Brokers)
	factory := func(dialCtx context.Context) (kafkaTopicAdmin, error) {
		errs := make([]error, 0, len(brokers))
		for _, broker := range brokers {
			conn, dialErr := kafka.DialContext(dialCtx, "tcp", broker)
			if dialErr == nil {
				return &kafkaConnTopicAdmin{conn: conn}, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", broker, dialErr))
		}
		return nil, fmt.Errorf("dial Kafka brokers: %w", errors.Join(errs...))
	}
	return ensureKafkaTopics(ctx, specs, factory, defaultKafkaProvisioningOptions())
}

func ensureKafkaTopics(
	ctx context.Context,
	specs []TopicSpec,
	factory kafkaTopicAdminFactory,
	options kafkaProvisioningOptions,
) error {
	if len(specs) == 0 {
		return errors.New("no Kafka topics configured")
	}
	if factory == nil {
		return errors.New("Kafka topic admin factory is nil")
	}
	if options.startupTimeout <= 0 || options.retryInterval <= 0 || options.metadataTimeout <= 0 || options.metadataPollInterval <= 0 {
		return errors.New("Kafka provisioning retry options must be positive")
	}

	startupCtx, cancel := context.WithTimeout(ctx, options.startupTimeout)
	defer cancel()
	var lastErr error
	for {
		admin, err := factory(startupCtx)
		if err == nil {
			err = provisionKafkaTopics(startupCtx, admin, specs, options)
			if closeErr := admin.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
			if err == nil {
				return nil
			}
		}
		if !isRetryableKafkaProvisioningError(err) {
			return err
		}
		lastErr = err
		select {
		case <-startupCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Kafka topic provisioning timed out: %w", lastErr)
		case <-time.After(options.retryInterval):
		}
	}
}

func provisionKafkaTopics(ctx context.Context, admin kafkaTopicAdmin, specs []TopicSpec, options kafkaProvisioningOptions) error {
	if admin == nil {
		return errors.New("Kafka topic admin is nil")
	}
	partitions, err := admin.ReadPartitions(ctx)
	if err != nil {
		return err
	}
	existing := make(map[string]struct{}, len(partitions))
	for _, partition := range partitions {
		if partition.Error != nil {
			return partition.Error
		}
		existing[partition.Topic] = struct{}{}
	}

	missing := make([]kafka.TopicConfig, 0, len(specs))
	for _, spec := range specs {
		if _, ok := existing[spec.Name]; ok {
			continue
		}
		missing = append(missing, kafka.TopicConfig{
			Topic:             spec.Name,
			NumPartitions:     spec.Partitions,
			ReplicationFactor: spec.ReplicationFactor,
		})
	}
	if len(missing) > 0 {
		if err := admin.CreateTopics(ctx, missing...); err != nil && !errors.Is(err, kafka.TopicAlreadyExists) {
			return err
		}
	}
	return verifyKafkaTopicMetadata(ctx, admin, specs, options)
}

func verifyKafkaTopicMetadata(ctx context.Context, admin kafkaTopicAdmin, specs []TopicSpec, options kafkaProvisioningOptions) error {
	metadataCtx, cancel := context.WithTimeout(ctx, options.metadataTimeout)
	defer cancel()
	var lastErr error
	for {
		allReady := true
		for _, spec := range specs {
			partitions, err := admin.ReadPartitions(metadataCtx, spec.Name)
			if err != nil {
				lastErr = err
				allReady = false
				break
			}
			if err := verifyTopicTopology(spec, partitions); err != nil {
				if errors.Is(err, errKafkaMetadataUnavailable) {
					lastErr = err
					allReady = false
					break
				}
				return err
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-metadataCtx.Done():
			if lastErr == nil {
				lastErr = errKafkaMetadataUnavailable
			}
			return fmt.Errorf("Kafka topic metadata was not ready: %w", lastErr)
		case <-time.After(options.metadataPollInterval):
		}
	}
}

var errKafkaMetadataUnavailable = errors.New("Kafka topic metadata unavailable")

type kafkaTopicTopologyError struct {
	topic  string
	reason string
}

func (e *kafkaTopicTopologyError) Error() string {
	return fmt.Sprintf("Kafka topic %q has unexpected topology: %s", e.topic, e.reason)
}

func verifyTopicTopology(spec TopicSpec, partitions []kafka.Partition) error {
	if len(partitions) == 0 {
		return errKafkaMetadataUnavailable
	}
	for _, partition := range partitions {
		if partition.Error != nil {
			return partition.Error
		}
	}
	if len(partitions) != spec.Partitions {
		return &kafkaTopicTopologyError{topic: spec.Name, reason: fmt.Sprintf("partitions=%d want=%d", len(partitions), spec.Partitions)}
	}
	for _, partition := range partitions {
		if replicas := len(partition.Replicas); replicas > 0 && replicas != spec.ReplicationFactor {
			return &kafkaTopicTopologyError{topic: spec.Name, reason: fmt.Sprintf("replication_factor=%d want=%d", replicas, spec.ReplicationFactor)}
		}
	}
	return nil
}

func isRetryableKafkaProvisioningError(err error) bool {
	if err == nil {
		return false
	}
	var topologyErr *kafkaTopicTopologyError
	if errors.As(err, &topologyErr) {
		return false
	}
	var kafkaErr kafka.Error
	if errors.As(err, &kafkaErr) {
		switch kafkaErr {
		case kafka.InvalidPartitionNumber,
			kafka.InvalidReplicationFactor,
			kafka.InvalidReplicaAssignment,
			kafka.InvalidConfiguration,
			kafka.TopicAuthorizationFailed,
			kafka.ClusterAuthorizationFailed,
			kafka.BrokerAuthorizationFailed:
			return false
		}
	}
	return true
}
