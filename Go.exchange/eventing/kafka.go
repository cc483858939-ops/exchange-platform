package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Publisher interface {
	Publish(context.Context, Envelope) error
	Close() error
}

type BatchPublisher interface {
	PublishBatch(context.Context, []Envelope) error
}

// RawPublisher publishes already-encoded Kafka messages to an explicit topic.
// It is intended for infrastructure paths such as DLQ replay.
type RawPublisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}

type KafkaPublisher struct {
	config        config.KafkaConfig
	mu            sync.Mutex
	writers       map[string]kafkaMessageWriter
	writerFactory kafkaWriterFactory
	state         kafkaPublisherState
	closeDone     chan struct{}
	closeErr      error
}

type kafkaMessageWriter interface {
	WriteMessages(context.Context, ...kafka.Message) error
	Close() error
}

type kafkaWriterFactory func(config.KafkaConfig, string) (kafkaMessageWriter, error)

type kafkaPublisherState uint8

const (
	kafkaPublisherActive kafkaPublisherState = iota
	kafkaPublisherClosing
	kafkaPublisherClosed
)

const (
	kafkaWriterMaxAttempts  = 2
	kafkaWriterReadTimeout  = 2 * time.Second
	kafkaWriterWriteTimeout = 2 * time.Second
	kafkaWriterBackoffMin   = 100 * time.Millisecond
	kafkaWriterBackoffMax   = 250 * time.Millisecond
	kafkaWriterBatchTimeout = 50 * time.Millisecond
)

var errKafkaPublisherClosed = errors.New("kafka publisher is closing or closed")

func NewKafkaPublisher(kafkaConfig config.KafkaConfig) (*KafkaPublisher, error) {
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	return &KafkaPublisher{
		config:        kafkaConfig,
		writers:       make(map[string]kafkaMessageWriter),
		writerFactory: newKafkaWriterFactory,
	}, nil
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
	if ctx == nil {
		return errors.New("Kafka publish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	byTopic := make(map[string][]kafka.Message)
	topics := make([]string, 0)
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
		if _, exists := byTopic[topic]; !exists {
			topics = append(topics, topic)
		}
		byTopic[topic] = append(byTopic[topic], kafka.Message{
			Key: []byte(KeyForEvent(event)), Value: value, Time: event.OccurredAt,
		})
	}
	sort.Strings(topics)
	for _, topic := range topics {
		if err := ctx.Err(); err != nil {
			return err
		}
		writer, err := p.writer(ctx, topic)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writer.WriteMessages(ctx, byTopic[topic]...); err != nil {
			return err
		}
	}
	return nil
}

func (p *KafkaPublisher) PublishRaw(ctx context.Context, topic string, messages ...kafka.Message) error {
	if p == nil {
		return errors.New("kafka publisher is nil")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("Kafka topic is required")
	}
	if len(messages) == 0 {
		return nil
	}
	if ctx == nil {
		return errors.New("Kafka publish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(normalizedBrokers(p.config.Brokers)) == 0 {
		return errors.New("kafka brokers are required")
	}
	writer, err := p.writer(ctx, topic)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writer.WriteMessages(ctx, messages...)
}

// PublishRawMessages is used only for infrastructure side channels such as a
// projection DLQ. Domain activity publication continues to use canonical
// Envelope values and TopicForEvent.
func PublishRawMessages(ctx context.Context, kafkaConfig config.KafkaConfig, topic string, messages ...kafka.Message) error {
	return publishRawMessages(ctx, kafkaConfig, topic, messages, newKafkaWriterFactory)
}

func publishRawMessages(ctx context.Context, kafkaConfig config.KafkaConfig, topic string, messages []kafka.Message, writerFactory kafkaWriterFactory) error {
	if strings.TrimSpace(topic) == "" {
		return errors.New("Kafka topic is required")
	}
	if len(messages) == 0 {
		return nil
	}
	if ctx == nil {
		return errors.New("Kafka publish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return errors.New("kafka brokers are required")
	}
	if writerFactory == nil {
		return errors.New("Kafka writer factory is nil")
	}
	writer, err := writerFactory(kafkaConfig, topic)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, writer.Close())
	}
	writeErr := writer.WriteMessages(ctx, messages...)
	closeErr := writer.Close()
	return errors.Join(writeErr, closeErr)
}

func newKafkaWriterFactory(kafkaConfig config.KafkaConfig, topic string) (kafkaMessageWriter, error) {
	return newKafkaWriter(kafkaConfig, topic)
}

func newKafkaWriter(kafkaConfig config.KafkaConfig, topic string) (*kafka.Writer, error) {
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	return &kafka.Writer{
		Addr:            kafka.TCP(brokers...),
		Topic:           topic,
		Balancer:        &kafka.Hash{},
		RequiredAcks:    kafka.RequireAll,
		Async:           false,
		BatchTimeout:    kafkaWriterBatchTimeout,
		MaxAttempts:     kafkaWriterMaxAttempts,
		ReadTimeout:     kafkaWriterReadTimeout,
		WriteTimeout:    kafkaWriterWriteTimeout,
		WriteBackoffMin: kafkaWriterBackoffMin,
		WriteBackoffMax: kafkaWriterBackoffMax,
	}, nil
}

func (p *KafkaPublisher) writer(ctx context.Context, topic string) (kafkaMessageWriter, error) {
	if p == nil {
		return nil, errors.New("kafka publisher is nil")
	}
	if ctx == nil {
		return nil, errors.New("Kafka publish context is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.state != kafkaPublisherActive {
		return nil, errKafkaPublisherClosed
	}
	if p.writers == nil {
		p.writers = make(map[string]kafkaMessageWriter)
	}
	writer := p.writers[topic]
	if writer == nil {
		factory := p.writerFactory
		if factory == nil {
			factory = newKafkaWriterFactory
		}
		var err error
		writer, err = factory(p.config, topic)
		if err != nil {
			return nil, fmt.Errorf("create Kafka writer for topic %q: %w", topic, err)
		}
		p.writers[topic] = writer
	}
	return writer, nil
}

func (p *KafkaPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.state == kafkaPublisherClosed {
		err := p.closeErr
		p.mu.Unlock()
		return err
	}
	if p.state == kafkaPublisherClosing {
		done := p.closeDone
		p.mu.Unlock()
		<-done
		p.mu.Lock()
		err := p.closeErr
		p.mu.Unlock()
		return err
	}
	p.state = kafkaPublisherClosing
	p.closeDone = make(chan struct{})
	done := p.closeDone
	topics := make([]string, 0, len(p.writers))
	for topic := range p.writers {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	writers := make([]kafkaMessageWriter, len(topics))
	for i, topic := range topics {
		writers[i] = p.writers[topic]
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	closeErrs := make([]error, len(writers))
	for i, writer := range writers {
		wg.Add(1)
		go func(i int, writer kafkaMessageWriter) {
			defer wg.Done()
			if err := writer.Close(); err != nil {
				closeErrs[i] = fmt.Errorf("close Kafka writer for topic %q: %w", topics[i], err)
			}
		}(i, writer)
	}
	wg.Wait()

	p.mu.Lock()
	p.closeErr = errors.Join(closeErrs...)
	p.state = kafkaPublisherClosed
	close(done)
	err := p.closeErr
	p.mu.Unlock()
	return err
}

func NewKafkaReader(kafkaConfig config.KafkaConfig, topic, groupID string) (*kafka.Reader, string, error) {
	topic = strings.TrimSpace(topic)
	groupID = strings.TrimSpace(groupID)
	brokers := normalizedBrokers(kafkaConfig.Brokers)
	if len(brokers) == 0 {
		return nil, "", errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(groupID) == "" {
		return nil, "", errors.New("kafka topic and group id are required")
	}
	clientID := "goexchange-" + uuid.NewString()
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, Topic: topic, GroupID: groupID,
		Dialer:   &kafka.Dialer{ClientID: clientID},
		MinBytes: 1, MaxBytes: 10e6, CommitInterval: 0, StartOffset: kafka.FirstOffset,
	})
	return reader, clientID, nil
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
