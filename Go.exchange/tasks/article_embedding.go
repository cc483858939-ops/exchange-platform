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
	"Go.exchange/consts"
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

const articleEmbeddingConsumerRestartDelay = 2 * time.Second

type articleEmbeddingMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type articleEmbeddingStore interface {
	GetArticle(context.Context, uint) (models.Article, error)
	GetEmbedding(context.Context, uint) (models.ArticleEmbedding, error)
	UpsertEmbedding(context.Context, models.ArticleEmbedding) error
}

type atomicArticleEmbeddingStore interface {
	UpsertEmbeddingAndInvalidateProfiles(context.Context, models.ArticleEmbedding, time.Time) error
}

type gormArticleEmbeddingStore struct {
	db *gorm.DB
}

func (s gormArticleEmbeddingStore) GetArticle(ctx context.Context, articleID uint) (models.Article, error) {
	var article models.Article
	if s.db == nil {
		return article, errors.New("database is not initialized")
	}
	err := s.db.WithContext(ctx).First(&article, articleID).Error
	return article, err
}

func (s gormArticleEmbeddingStore) GetEmbedding(ctx context.Context, articleID uint) (models.ArticleEmbedding, error) {
	var embedding models.ArticleEmbedding
	if s.db == nil {
		return embedding, errors.New("database is not initialized")
	}
	err := s.db.WithContext(ctx).Where("article_id = ?", articleID).First(&embedding).Error
	return embedding, err
}

func (s gormArticleEmbeddingStore) UpsertEmbedding(ctx context.Context, embedding models.ArticleEmbedding) error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "article_id"}},
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
func (s gormArticleEmbeddingStore) UpsertEmbeddingAndInvalidateProfiles(ctx context.Context, embedding models.ArticleEmbedding, now time.Time) error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "article_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"version": embedding.Version, "model": embedding.Model, "dimensions": embedding.Dimensions,
				"embedding": embedding.Embedding, "content_hash": embedding.ContentHash, "updated_at": embedding.UpdatedAt,
			}),
		}).Create(&embedding).Error; err != nil {
			return err
		}
		var users []uint
		if err := tx.Raw(`
SELECT user_id FROM article_behaviors WHERE article_id = ?
UNION
SELECT user_id FROM article_reaction WHERE article_id = ?`, embedding.ArticleID, embedding.ArticleID).Scan(&users).Error; err != nil {
			return err
		}
		return recommendation.InvalidateProfiles(tx, users, "article_embedding_changed", now)
	})
}

var newArticleEmbedder = func(cfg config.EmbeddingConfig) (embeddings.Embedder, error) {
	return embeddings.NewOpenAICompatibleEmbedder(cfg)
}

var newArticleEmbeddingReader = func(cfg config.KafkaConfig, topic, groupID string) (articleEmbeddingMessageReader, error) {
	return eventing.NewKafkaReader(cfg, topic, groupID)
}

func startArticleEmbeddingConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled ||
		strings.TrimSpace(config.AppConfig.Kafka.ArticleEmbeddingTopic) == "" ||
		strings.TrimSpace(config.AppConfig.Kafka.ArticleEmbeddingGroupID) == "" {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			runArticleEmbeddingConsumer(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(articleEmbeddingConsumerRestartDelay):
			}
		}
	}()
}

func runArticleEmbeddingConsumer(ctx context.Context) {
	if config.AppConfig == nil {
		return
	}
	if global.Db == nil {
		log.Printf("[ArticleEmbedding] consumer disabled: database is not initialized")
		return
	}
	activeVersion := strings.TrimSpace(config.ActiveEmbeddingVersion())
	if activeVersion == "" {
		log.Printf("[ArticleEmbedding] consumer disabled: active embedding version is empty")
		return
	}
	embedder, err := newArticleEmbedder(config.AppConfig.Embedding)
	if err != nil {
		log.Printf("[ArticleEmbedding] create embedder: %v", err)
		return
	}
	topic := strings.TrimSpace(config.AppConfig.Kafka.ArticleEmbeddingTopic)
	groupID := strings.TrimSpace(config.AppConfig.Kafka.ArticleEmbeddingGroupID)
	reader, err := newArticleEmbeddingReader(config.AppConfig.Kafka, topic, groupID)
	if err != nil {
		log.Printf("[ArticleEmbedding] create Kafka reader: %v", err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			log.Printf("[ArticleEmbedding] close Kafka reader: %v", closeErr)
		}
	}()
	store := gormArticleEmbeddingStore{db: global.Db}
	if err := consumeArticleEmbeddingMessages(ctx, reader, embedder, store, activeVersion); err != nil && ctx.Err() == nil {
		log.Printf("[ArticleEmbedding] consume: %v", err)
	}
}

func consumeArticleEmbeddingMessages(ctx context.Context, reader articleEmbeddingMessageReader, embedder embeddings.Embedder, store articleEmbeddingStore, activeVersion string) error {
	if reader == nil {
		return errors.New("article embedding message reader is nil")
	}
	if embedder == nil {
		return errors.New("article embedding provider is nil")
	}
	if store == nil {
		return errors.New("article embedding store is nil")
	}
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		started := time.Now()
		processErr := processArticleEmbeddingMessage(ctx, message, embedder, store, activeVersion)
		metrics.ObserveArticleEmbeddingProcessingDuration(time.Since(started))
		if processErr != nil {
			return processErr
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			metrics.RecordArticleEmbeddingFailure("kafka_commit")
			return err
		}
	}
}

func processArticleEmbeddingMessage(ctx context.Context, message kafka.Message, embedder embeddings.Embedder, store articleEmbeddingStore, activeVersion string) error {
	articleID, err := decodeArticleEmbeddingRequest(message.Value)
	if err != nil {
		log.Printf("[ArticleEmbedding] discard poison message: %v", err)
		metrics.RecordArticleEmbeddingFailure("decode")
		metrics.RecordArticleEmbeddingEvent("invalid_event")
		return nil
	}
	if store == nil {
		return errors.New("article embedding store is nil")
	}
	activeVersion = strings.TrimSpace(activeVersion)
	if activeVersion == "" {
		return errors.New("active embedding version is required")
	}
	article, err := store.GetArticle(ctx, articleID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			metrics.RecordArticleEmbeddingEvent("article_missing")
			return nil
		}
		metrics.RecordArticleEmbeddingFailure("db_read")
		return err
	}
	if article.PublicationState != consts.ArticlePublicationStatePublished || article.PublishedAt == nil {
		metrics.RecordArticleEmbeddingEvent("article_unavailable")
		return nil
	}

	text := embeddings.BuildArticleEmbeddingText(article.Title, article.Content)
	contentHash := embeddings.ArticleEmbeddingContentHash(article.Title, article.Content)

	existing, lookupErr := store.GetEmbedding(ctx, articleID)
	if lookupErr == nil && existing.Version == activeVersion && existing.ContentHash == contentHash {
		metrics.RecordArticleEmbeddingEvent("up_to_date")
		return nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		metrics.RecordArticleEmbeddingFailure("db_read")
		return lookupErr
	}

	result, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if embeddings.IsRetryableProviderError(err) {
			metrics.RecordArticleEmbeddingFailure("provider")
			return err
		}
		log.Printf("[ArticleEmbedding] permanent provider error article=%d: %v", articleID, err)
		metrics.RecordArticleEmbeddingFailure("provider")
		metrics.RecordArticleEmbeddingEvent("provider_non_retryable")
		return nil
	}
	if len(result.Vectors) != 1 || !validArticleEmbeddingVector(result.Vectors[0]) {
		metrics.RecordArticleEmbeddingFailure("provider")
		return errors.New("embedding provider returned an invalid single vector")
	}
	modelName := strings.TrimSpace(result.Model)
	if modelName == "" {
		metrics.RecordArticleEmbeddingFailure("provider")
		return errors.New("embedding provider returned an empty model")
	}
	dimensions := len(result.Vectors[0])
	if dimensions <= 0 {
		metrics.RecordArticleEmbeddingFailure("provider")
		return errors.New("embedding provider returned invalid dimensions")
	}

	now := time.Now().UTC()
	vector := pgvector.NewVector(result.Vectors[0])
	embedding := models.ArticleEmbedding{
		ArticleID: articleID, Version: activeVersion, Model: modelName,
		Dimensions: dimensions, Embedding: vector, ContentHash: contentHash,
		CreatedAt: now, UpdatedAt: now,
	}
	var upsertErr error
	if atomicStore, ok := store.(atomicArticleEmbeddingStore); ok {
		upsertErr = atomicStore.UpsertEmbeddingAndInvalidateProfiles(ctx, embedding, now)
	} else {
		upsertErr = store.UpsertEmbedding(ctx, embedding)
	}
	if upsertErr != nil {
		metrics.RecordArticleEmbeddingFailure("db_upsert")
		return upsertErr
	}
	metrics.RecordArticleEmbeddingEvent("generated")
	return nil
}

func decodeArticleEmbeddingRequest(raw []byte) (uint, error) {
	event, err := eventing.DecodeEnvelope(raw)
	if err != nil {
		return 0, err
	}
	if _, err := uuid.Parse(event.ID); err != nil {
		return 0, errors.New("article embedding event id must be a UUID")
	}
	if event.Type != eventing.EventTypeArticleEmbeddingRequested {
		return 0, fmt.Errorf("unexpected article embedding event type %q", event.Type)
	}
	if event.SchemaVersion != 1 {
		return 0, fmt.Errorf("unsupported article embedding schema version %d", event.SchemaVersion)
	}
	if event.AggregateType != "article" {
		return 0, fmt.Errorf("unexpected article embedding aggregate type %q", event.AggregateType)
	}
	if event.OccurredAt.IsZero() {
		return 0, errors.New("article embedding occurred_at is required")
	}
	var payload eventing.ArticleEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return 0, fmt.Errorf("decode article embedding payload: %w", err)
	}
	if payload.ArticleID == 0 {
		return 0, errors.New("article embedding payload article_id is required")
	}
	if event.AggregateID != strconv.FormatUint(uint64(payload.ArticleID), 10) {
		return 0, errors.New("article embedding aggregate_id does not match payload article_id")
	}
	return payload.ArticleID, nil
}

func validArticleEmbeddingVector(vector []float32) bool {
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
