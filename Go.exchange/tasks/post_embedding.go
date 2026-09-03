package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const postEmbeddingConsumerRestartDelay = 2 * time.Second

type postEmbeddingMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type postEmbeddingStore interface {
	GetPost(context.Context, uint) (models.Post, error)
	GetEmbedding(context.Context, uint) (models.PostEmbedding, error)
	UpsertEmbedding(context.Context, models.PostEmbedding) error
}

type atomicPostEmbeddingStore interface {
	UpsertEmbeddingAndInvalidateProfiles(context.Context, models.PostEmbedding, time.Time) error
}

type gormPostEmbeddingStore struct {
	db *gorm.DB
}

func (s gormPostEmbeddingStore) GetPost(ctx context.Context, postID uint) (models.Post, error) {
	var post models.Post
	if s.db == nil {
		return post, errors.New("database is not initialized")
	}
	err := s.db.WithContext(ctx).First(&post, postID).Error
	return post, err
}

func (s gormPostEmbeddingStore) GetEmbedding(ctx context.Context, postID uint) (models.PostEmbedding, error) {
	var embedding models.PostEmbedding
	if s.db == nil {
		return embedding, errors.New("database is not initialized")
	}
	err := s.db.WithContext(ctx).Where("post_id = ?", postID).First(&embedding).Error
	return embedding, err
}

func (s gormPostEmbeddingStore) UpsertEmbedding(ctx context.Context, embedding models.PostEmbedding) error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "post_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"version": embedding.Version, "model": embedding.Model, "dimensions": embedding.Dimensions,
			"embedding": embedding.Embedding, "content_hash": embedding.ContentHash, "updated_at": embedding.UpdatedAt,
		}),
	}).Create(&embedding).Error
}

// UpsertEmbeddingAndInvalidateProfiles commits the active embedding and its
// authoritative user fan-out in one transaction. The fan-out intentionally
// reads source behavior and reaction tables because the canonical state table
// may not exist yet for a first interaction.
func (s gormPostEmbeddingStore) UpsertEmbeddingAndInvalidateProfiles(ctx context.Context, embedding models.PostEmbedding, now time.Time) error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "post_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"version": embedding.Version, "model": embedding.Model, "dimensions": embedding.Dimensions,
				"embedding": embedding.Embedding, "content_hash": embedding.ContentHash, "updated_at": embedding.UpdatedAt,
			}),
		}).Create(&embedding).Error; err != nil {
			return err
		}
		var users []uint
		if err := tx.Raw(`
SELECT user_id FROM post_behaviors WHERE post_id = ?
UNION
SELECT user_id FROM post_reaction WHERE post_id = ?`, embedding.PostID, embedding.PostID).Scan(&users).Error; err != nil {
			return err
		}
		return recommendation.InvalidateProfiles(tx, users, "post_embedding_changed", now)
	})
}

var newPostEmbedder = func(cfg config.EmbeddingConfig) (embeddings.Embedder, error) {
	return embeddings.NewOpenAICompatibleEmbedder(cfg)
}

var newPostEmbeddingReader = func(cfg config.KafkaConfig, topic, groupID string) (postEmbeddingMessageReader, error) {
	return eventing.NewKafkaReader(cfg, topic, groupID)
}

func startPostEmbeddingConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled ||
		strings.TrimSpace(config.AppConfig.Kafka.PostEmbeddingTopic) == "" ||
		strings.TrimSpace(config.AppConfig.Kafka.PostEmbeddingGroupID) == "" {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelinePostEmbedding)
		defer PipelineStopped(PipelinePostEmbedding)
		for {
			runPostEmbeddingConsumer(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(postEmbeddingConsumerRestartDelay):
			}
		}
	}()
}

func runPostEmbeddingConsumer(ctx context.Context) {
	if config.AppConfig == nil {
		return
	}
	if global.Db == nil {
		PipelineFailure(PipelinePostEmbedding, "database_unavailable", 0)
		log.Printf("[PostEmbedding] consumer disabled: database is not initialized")
		return
	}
	activeVersion := strings.TrimSpace(config.ActiveEmbeddingVersion())
	if activeVersion == "" {
		PipelineFailure(PipelinePostEmbedding, "embedding_config_invalid", 0)
		log.Printf("[PostEmbedding] consumer disabled: active embedding version is empty")
		return
	}
	embedder, err := newPostEmbedder(config.AppConfig.Embedding)
	if err != nil {
		PipelineFailure(PipelinePostEmbedding, "embedding_provider_unavailable", 0)
		log.Printf("[PostEmbedding] create embedder: %v", err)
		return
	}
	topic := strings.TrimSpace(config.AppConfig.Kafka.PostEmbeddingTopic)
	groupID := strings.TrimSpace(config.AppConfig.Kafka.PostEmbeddingGroupID)
	reader, err := newPostEmbeddingReader(config.AppConfig.Kafka, topic, groupID)
	if err != nil {
		PipelineFailure(PipelinePostEmbedding, "kafka_reader_unavailable", 0)
		log.Printf("[PostEmbedding] create Kafka reader: %v", err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			log.Printf("[PostEmbedding] close Kafka reader: %v", closeErr)
		}
	}()
	store := gormPostEmbeddingStore{db: global.Db}
	if err := consumePostEmbeddingMessages(ctx, reader, embedder, store, activeVersion); err != nil && ctx.Err() == nil {
		PipelineFailure(PipelinePostEmbedding, "projection_failed", 0)
		log.Printf("[PostEmbedding] consume: %v", err)
	}
}

func consumePostEmbeddingMessages(ctx context.Context, reader postEmbeddingMessageReader, embedder embeddings.Embedder, store postEmbeddingStore, activeVersion string) error {
	if reader == nil {
		return errors.New("post embedding message reader is nil")
	}
	if embedder == nil {
		return errors.New("post embedding provider is nil")
	}
	if store == nil {
		return errors.New("post embedding store is nil")
	}
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		started := time.Now()
		processErr := processPostEmbeddingMessage(ctx, message, embedder, store, activeVersion)
		metrics.ObservePostEmbeddingProcessingDuration(time.Since(started))
		if processErr != nil {
			return processErr
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			metrics.RecordPostEmbeddingFailure("kafka_commit")
			return err
		}
		backlog := int64(0)
		if statsReader, ok := reader.(interface{ Stats() kafka.ReaderStats }); ok {
			backlog = kafkaBacklog(statsReader)
		}
		PipelineCommit(PipelinePostEmbedding, time.Now().UTC(), backlog)
	}
}

func processPostEmbeddingMessage(ctx context.Context, message kafka.Message, embedder embeddings.Embedder, store postEmbeddingStore, activeVersion string) error {
	postID, err := decodePostEmbeddingRequest(message.Value)
	if err != nil {
		log.Printf("[PostEmbedding] discard poison message: %v", err)
		metrics.RecordPostEmbeddingFailure("decode")
		metrics.RecordPostEmbeddingEvent("invalid_event")
		return nil
	}
	if store == nil {
		return errors.New("post embedding store is nil")
	}
	activeVersion = strings.TrimSpace(activeVersion)
	if activeVersion == "" {
		return errors.New("active embedding version is required")
	}
	post, err := store.GetPost(ctx, postID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			metrics.RecordPostEmbeddingEvent("post_missing")
			return nil
		}
		metrics.RecordPostEmbeddingFailure("db_read")
		return err
	}
	text := embeddings.BuildPostEmbeddingText(post.Content)
	contentHash := embeddings.PostEmbeddingContentHash(post.Content)

	existing, lookupErr := store.GetEmbedding(ctx, postID)
	if lookupErr == nil && existing.Version == activeVersion && existing.ContentHash == contentHash {
		metrics.RecordPostEmbeddingEvent("up_to_date")
		return nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		metrics.RecordPostEmbeddingFailure("db_read")
		return lookupErr
	}

	result, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if embeddings.IsRetryableProviderError(err) {
			metrics.RecordPostEmbeddingFailure("provider")
			return err
		}
		log.Printf("[PostEmbedding] permanent provider error post=%d: %v", postID, err)
		metrics.RecordPostEmbeddingFailure("provider")
		metrics.RecordPostEmbeddingEvent("provider_non_retryable")
		return nil
	}
	if len(result.Vectors) != 1 || !validPostEmbeddingVector(result.Vectors[0]) {
		metrics.RecordPostEmbeddingFailure("provider")
		return errors.New("embedding provider returned an invalid single vector")
	}
	modelName := strings.TrimSpace(result.Model)
	if modelName == "" {
		metrics.RecordPostEmbeddingFailure("provider")
		return errors.New("embedding provider returned an empty model")
	}
	dimensions := len(result.Vectors[0])
	if dimensions <= 0 {
		metrics.RecordPostEmbeddingFailure("provider")
		return errors.New("embedding provider returned invalid dimensions")
	}

	now := time.Now().UTC()
	vector := pgvector.NewVector(result.Vectors[0])
	embedding := models.PostEmbedding{
		PostID: postID, Version: activeVersion, Model: modelName,
		Dimensions: dimensions, Embedding: vector, ContentHash: contentHash,
		CreatedAt: now, UpdatedAt: now,
	}
	var upsertErr error
	if atomicStore, ok := store.(atomicPostEmbeddingStore); ok {
		upsertErr = atomicStore.UpsertEmbeddingAndInvalidateProfiles(ctx, embedding, now)
	} else {
		upsertErr = store.UpsertEmbedding(ctx, embedding)
	}
	if upsertErr != nil {
		metrics.RecordPostEmbeddingFailure("db_upsert")
		return upsertErr
	}
	metrics.RecordPostEmbeddingEvent("generated")
	return nil
}

func decodePostEmbeddingRequest(raw []byte) (uint, error) {
	event, err := eventing.DecodeEnvelope(raw)
	if err != nil {
		return 0, err
	}
	if _, err := uuid.Parse(event.ID); err != nil {
		return 0, errors.New("post embedding event id must be a UUID")
	}
	if event.Type != eventing.EventTypePostEmbeddingRequested {
		return 0, fmt.Errorf("unexpected post embedding event type %q", event.Type)
	}
	if event.SchemaVersion != 1 {
		return 0, fmt.Errorf("unsupported post embedding schema version %d", event.SchemaVersion)
	}
	if event.AggregateType != "post" {
		return 0, fmt.Errorf("unexpected post embedding aggregate type %q", event.AggregateType)
	}
	if event.OccurredAt.IsZero() {
		return 0, errors.New("post embedding occurred_at is required")
	}
	var payload eventing.PostEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return 0, fmt.Errorf("decode post embedding payload: %w", err)
	}
	if payload.PostID == 0 {
		return 0, errors.New("post embedding payload post_id is required")
	}
	if event.AggregateID != strconv.FormatUint(uint64(payload.PostID), 10) {
		return 0, errors.New("post embedding aggregate_id does not match payload post_id")
	}
	return payload.PostID, nil
}

func validPostEmbeddingVector(vector []float32) bool {
	if len(vector) == 0 {
		return false
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}
