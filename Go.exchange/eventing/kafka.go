package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"

	"github.com/segmentio/kafka-go"
)

type Publisher interface {
	Publish(context.Context, Envelope) error
	Close() error
}

type BatchPublisher interface {
	PublishBatch(context.Context, []Envelope) error
}

type KafkaPublisher struct {
	config  config.KafkaConfig
	mu      sync.Mutex
	writers map[string]*kafka.Writer
}

func NewKafkaPublisher(kafkaConfig config.KafkaConfig) (*KafkaPublisher, error) {
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	return &KafkaPublisher{config: kafkaConfig, writers: make(map[string]*kafka.Writer)}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, event Envelope) error {
	return p.PublishBatch(ctx, []Envelope{event})
}

func (p *KafkaPublisher) PublishBatch(ctx context.Context, events []Envelope) error {
	if p == nil {
		return errors.New("kafka publisher is nil")
	}
	if len(events) == 0 {
		return nil
	}
	byTopic := make(map[string][]kafka.Message)
	for _, event := range events {
		topic, err := TopicForEvent(p.config, event.Type)
		if err != nil {
			return err
		}
		if topic == "" {
			return fmt.Errorf("Kafka topic is empty for event type %q", event.Type)
		}
		value, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal Kafka event: %w", err)
		}
		byTopic[topic] = append(byTopic[topic], kafka.Message{
			Key: []byte(KeyForEvent(event)), Value: value, Time: event.OccurredAt,
		})
	}
	for topic, messages := range byTopic {
		if err := p.writer(topic).WriteMessages(ctx, messages...); err != nil {
			return err
		}
	}
	return nil
}

// PublishRawMessages is used only for infrastructure side channels such as a
// projection DLQ. Domain activity publication continues to use canonical
// Envelope values and TopicForEvent.
func PublishRawMessages(ctx context.Context, kafkaConfig config.KafkaConfig, topic string, messages ...kafka.Message) error {
	if strings.TrimSpace(topic) == "" {
		return errors.New("Kafka topic is required")
	}
	if len(messages) == 0 {
		return nil
	}
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return errors.New("kafka brokers are required")
	}
	writer := &kafka.Writer{
		Addr: kafka.TCP(brokers...), Topic: topic, Balancer: &kafka.Hash{},
		RequiredAcks: kafka.RequireAll, Async: false, BatchTimeout: 50 * time.Millisecond,
	}
	defer writer.Close()
	return writer.WriteMessages(ctx, messages...)
}

func (p *KafkaPublisher) writer(topic string) *kafka.Writer {
	p.mu.Lock()
	defer p.mu.Unlock()
	writer := p.writers[topic]
	if writer == nil {
		writer = &kafka.Writer{
			Addr:         kafka.TCP(normalizedBrokers(p.config.Brokers)...),
			Topic:        topic,
			Balancer:     &kafka.Hash{},
			RequiredAcks: kafka.RequireAll,
			Async:        false,
			BatchTimeout: 50 * time.Millisecond,
		}
		p.writers[topic] = writer
	}
	return writer
}

func (p *KafkaPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for _, writer := range p.writers {
		if err := writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func NewKafkaReader(kafkaConfig config.KafkaConfig, topic, groupID string) (*kafka.Reader, error) {
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(groupID) == "" {
		return nil, errors.New("kafka topic and group id are required")
	}
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, Topic: topic, GroupID: groupID,
		MinBytes: 1, MaxBytes: 10e6, CommitInterval: 0, StartOffset: kafka.FirstOffset,
	}), nil
}

func KafkaReachable(ctx context.Context, kafkaConfig config.KafkaConfig) error {
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return errors.New("kafka brokers are required")
	}
	connection, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return err
	}
	return connection.Close()
}

func normalizedBrokers(brokers []string) []string {
	normalized := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		if broker = strings.TrimSpace(broker); broker != "" {
			normalized = append(normalized, broker)
		}
	}
	return normalized
}
