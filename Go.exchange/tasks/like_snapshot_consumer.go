package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type likeSnapshotMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type likeSnapshotApplyFunc func(context.Context, *gorm.DB, eventing.Envelope, eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error)

type likeSnapshotApplyOutcome uint8

const (
	likeSnapshotApplied likeSnapshotApplyOutcome = iota
	likeSnapshotNoop
)

func startLikeSnapshotProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelineLikeSnapshotProjection)
		defer PipelineStopped(PipelineLikeSnapshotProjection)
		for {
			runLikeSnapshotProjectionConsumer(ctx)
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func runLikeSnapshotProjectionConsumer(ctx context.Context) {
	kafkaConfig := config.AppConfig.Kafka
	reader, err := eventing.NewKafkaReader(kafkaConfig, kafkaConfig.LikeSnapshotTopic, kafkaConfig.LikeSnapshotGroupID)
	if err != nil {
		PipelineFailure(PipelineLikeSnapshotProjection, "kafka_reader_unavailable", 0)
		log.Printf("[LikeSnapshotProjection] create reader: %v", err)
		return
	}
	likeSnapshotConsumers.Add(1)
	defer likeSnapshotConsumers.Add(-1)
	publisher := eventingRawKafkaMessagePublisher{kafkaConfig: kafkaConfig}
	_ = consumeLikeSnapshotMessages(ctx, reader, publisher, global.Db, kafkaConfig)
}

func consumeLikeSnapshotMessages(ctx context.Context, reader likeSnapshotMessageReader, publisher rawKafkaMessagePublisher, db *gorm.DB, kafkaConfig config.KafkaConfig) error {
	return consumeLikeSnapshotMessagesWithApply(ctx, reader, publisher, db, kafkaConfig, defaultKafkaRetryPolicy, applyLikeSnapshotPayload)
}

func consumeLikeSnapshotMessagesWithApply(
	ctx context.Context,
	reader likeSnapshotMessageReader,
	publisher rawKafkaMessagePublisher,
	db *gorm.DB,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
	apply likeSnapshotApplyFunc,
) error {
	if reader == nil {
		return errors.New("like snapshot Kafka reader is nil")
	}
	defer reader.Close()
	if ctx == nil {
		return errors.New("like snapshot consumer context is nil")
	}
	if apply == nil {
		return errors.New("like snapshot apply function is nil")
	}

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				PipelineFailure(PipelineLikeSnapshotProjection, "kafka_fetch_failed", 0)
				log.Printf("[LikeSnapshotProjection] fetch: %v", err)
				return err
			}
			return ctx.Err()
		}
		if err := processLikeSnapshotMessageWithApply(ctx, db, publisher, kafkaConfig, message, policy, apply); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			PipelineFailure(PipelineLikeSnapshotProjection, "projection_failed", 0)
			log.Printf("[LikeSnapshotProjection] process topic=%s partition=%d offset=%d: %v", message.Topic, message.Partition, message.Offset, err)
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			if ctx.Err() == nil {
				PipelineFailure(PipelineLikeSnapshotProjection, "kafka_commit_failed", 0)
				commitErr := retryableKafkaError(kafkaFailureCodeKafkaCommit, err)
				metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(commitErr))
				log.Printf("[LikeSnapshotProjection] commit topic=%s partition=%d offset=%d code=%s: %v", message.Topic, message.Partition, message.Offset, kafkaFailureCode(commitErr), err)
				return commitErr
			}
			return ctx.Err()
		}
		backlog := int64(0)
		if statsReader, ok := reader.(interface{ Stats() kafka.ReaderStats }); ok {
			backlog = kafkaBacklog(statsReader)
		}
		PipelineCommit(PipelineLikeSnapshotProjection, time.Now().UTC(), backlog)
	}
}

func processLikeSnapshotMessage(ctx context.Context, db *gorm.DB, publisher rawKafkaMessagePublisher, kafkaConfig config.KafkaConfig, message kafka.Message) error {
	return processLikeSnapshotMessageWithApply(ctx, db, publisher, kafkaConfig, message, defaultKafkaRetryPolicy, applyLikeSnapshotPayload)
}

func processLikeSnapshotMessageWithApply(
	ctx context.Context,
	db *gorm.DB,
	publisher rawKafkaMessagePublisher,
	kafkaConfig config.KafkaConfig,
	message kafka.Message,
	policy kafkaRetryPolicy,
	apply likeSnapshotApplyFunc,
) error {
	if ctx == nil {
		return errors.New("like snapshot processing context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if apply == nil {
		return errors.New("like snapshot apply function is nil")
	}
	event, payload, err := decodeLikeSnapshotMessage(message)
	if err != nil {
		if kafkaFailureClassOf(err) != kafkaFailurePermanent {
			return err
		}
		log.Printf("[LikeSnapshotProjection] permanent message isolated topic=%s partition=%d offset=%d code=%s", message.Topic, message.Partition, message.Offset, kafkaFailureCode(err))
		if publishErr := publishConsumerDLQ(ctx, publisher, kafkaConfig.ConsumerDLQTopic, kafkaConsumerLikeSnapshotProjection, message, err, 1); publishErr != nil {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(publishErr))
			return publishErr
		}
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCode(err))
		return nil
	}

	attempts := 0
	var outcome likeSnapshotApplyOutcome
	err = retryKafkaOperation(ctx, policy, func(attempt int, retryErr error) {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCode(retryErr))
		log.Printf("[LikeSnapshotProjection] retry attempt=%d max_attempts=%d code=%s", attempt, policy.MaxAttempts, kafkaFailureCode(retryErr))
	}, func() error {
		attempts++
		outcome, err = apply(ctx, db, event, payload)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if kafkaFailureClassOf(err) == kafkaFailureRetryable && attempts >= policy.MaxAttempts {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCode(err))
		}
		return err
	}
	if outcome == likeSnapshotNoop {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
	} else {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerLikeSnapshotProjection, kafkaRecoveryOutcomeMessageApplied, kafkaRecoveryCodeNone)
	}
	return nil
}

func decodeLikeSnapshotMessage(message kafka.Message) (eventing.Envelope, eventing.PostLikeSnapshotPayload, error) {
	event, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return eventing.Envelope{}, eventing.PostLikeSnapshotPayload{}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, err)
	}
	payload, err := decodeLikeSnapshotEvent(event)
	if err != nil {
		return eventing.Envelope{}, eventing.PostLikeSnapshotPayload{}, err
	}
	return event, payload, nil
}

func decodeLikeSnapshotEvent(event eventing.Envelope) (eventing.PostLikeSnapshotPayload, error) {
	if event.Type != eventing.EventTypePostLikeSnapshot {
		return eventing.PostLikeSnapshotPayload{}, permanentKafkaError(kafkaFailureCodeUnsupportedEvent, fmt.Errorf("unsupported event type %q", event.Type))
	}
	if event.SchemaVersion != 1 {
		return eventing.PostLikeSnapshotPayload{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, fmt.Errorf("unsupported schema version %d", event.SchemaVersion))
	}
	var payload eventing.PostLikeSnapshotPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return eventing.PostLikeSnapshotPayload{}, permanentKafkaError(kafkaFailureCodeDecodePayload, err)
	}
	if strings.TrimSpace(event.ID) == "" || payload.PostID == 0 || payload.Version <= 0 || payload.LikeCount < 0 {
		return eventing.PostLikeSnapshotPayload{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("like snapshot payload is missing required fields or has invalid values"))
	}
	return payload, nil
}

func applyLikeSnapshotEvent(ctx context.Context, db *gorm.DB, event eventing.Envelope) (likeSnapshotApplyOutcome, error) {
	payload, err := decodeLikeSnapshotEvent(event)
	if err != nil {
		return likeSnapshotNoop, err
	}
	return applyLikeSnapshotPayload(ctx, db, event, payload)
}

func applyLikeSnapshotPayload(ctx context.Context, db *gorm.DB, event eventing.Envelope, payload eventing.PostLikeSnapshotPayload) (likeSnapshotApplyOutcome, error) {
	if db == nil {
		return likeSnapshotNoop, retryableKafkaError(kafkaFailureCodeDatabaseUnavailable, errors.New("database is unavailable"))
	}
	if ctx == nil {
		return likeSnapshotNoop, errors.New("like snapshot database context is nil")
	}
	if err := ctx.Err(); err != nil {
		return likeSnapshotNoop, err
	}
	consumerGroup := ""
	if config.AppConfig != nil {
		consumerGroup = config.AppConfig.Kafka.LikeSnapshotGroupID
	}
	outcome := likeSnapshotNoop
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		first, err := eventing.MarkInboxProcessed(tx, consumerGroup, event.ID)
		if err != nil {
			return err
		}
		if !first {
			outcome = likeSnapshotNoop
			return nil
		}
		result := tx.Model(&models.Post{}).
			Where("id = ? AND like_sync_version < ?", payload.PostID, payload.Version).
			Updates(map[string]interface{}{"like_count": payload.LikeCount, "like_sync_version": payload.Version})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			outcome = likeSnapshotNoop
			return nil
		}
		outcome = likeSnapshotApplied
		return nil
	})
	if err != nil {
		return likeSnapshotNoop, retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err)
	}
	return outcome, nil
}
