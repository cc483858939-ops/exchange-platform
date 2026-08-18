package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
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

	"github.com/pgvector/pgvector-go"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	articleEmbeddingRetryDelay           = 2 * time.Second
	articleEmbeddingConsumerRestartDelay = articleEmbeddingRetryDelay
)

type articleEmbeddingMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
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
	if err := consumeArticleEmbeddingMessages(ctx, reader, embedder); err != nil && ctx.Err() == nil {
		log.Printf("[ArticleEmbedding] consume: %v", err)
	}
}

func consumeArticleEmbeddingMessages(ctx context.Context, reader articleEmbeddingMessageReader, embedder embeddings.Embedder) error {
	if reader == nil {
		return errors.New("article embedding message reader is nil")
	}
	if embedder == nil {
		return errors.New("article embedding provider is nil")
	}
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		started := time.Now()
		processErr := processArticleEmbeddingMessage(ctx, message, embedder)
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

func processArticleEmbeddingMessage(ctx context.Context, message kafka.Message, embedder embeddings.Embedder) error {
	articleID, err := decodeArticleEmbeddingRequest(message.Value)
	if err != nil {
		log.Printf("[ArticleEmbedding] discard poison message: %v", err)
		metrics.RecordArticleEmbeddingFailure("decode")
		metrics.RecordArticleEmbeddingEvent("invalid_event")
		return nil
	}
	if global.Db == nil {
		err := errors.New("database is not initialized")
		metrics.RecordArticleEmbeddingFailure("article_load")
		return err
	}

	var article models.Article
	if err := global.Db.First(&article, articleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			metrics.RecordArticleEmbeddingEvent("article_missing")
			return nil
		}
		metrics.RecordArticleEmbeddingFailure("article_load")
		return err
	}
	if article.PublicationState != consts.ArticlePublicationStatePublished || article.PublishedAt == nil {
		metrics.RecordArticleEmbeddingEvent("article_unavailable")
		return nil
	}

	activeVersion := config.ActiveEmbeddingVersion()
	if strings.TrimSpace(activeVersion) == "" {
		return errors.New("active embedding version is required")
	}
	text := embeddings.BuildArticleEmbeddingText(article.Title, article.Content)
	contentHash := embeddings.ArticleEmbeddingContentHash(article.Title, article.Content)

	var existing models.ArticleEmbedding
	lookupErr := global.Db.Where("article_id = ?", articleID).First(&existing).Error
	if lookupErr == nil && existing.Version == activeVersion && existing.ContentHash == contentHash {
		metrics.RecordArticleEmbeddingEvent("up_to_date")
		return nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		metrics.RecordArticleEmbeddingFailure("db_upsert")
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
	if err := global.Db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "article_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"version": activeVersion, "model": modelName, "dimensions": dimensions,
			"embedding": vector, "content_hash": contentHash, "updated_at": now,
		}),
	}).Create(&embedding).Error; err != nil {
		metrics.RecordArticleEmbeddingFailure("db_upsert")
		return err
	}
	metrics.RecordArticleEmbeddingEvent("generated")
	return nil
}

func decodeArticleEmbeddingRequest(raw []byte) (uint, error) {
	event, err := eventing.DecodeEnvelope(raw)
	if err != nil {
		return 0, err
	}
	if event.Type != eventing.EventTypeArticleEmbeddingRequested {
		return 0, fmt.Errorf("unexpected article embedding event type %q", event.Type)
	}
	var payload eventing.ArticleEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return 0, fmt.Errorf("decode article embedding payload: %w", err)
	}
	if payload.ArticleID == 0 {
		return 0, errors.New("article embedding payload article_id is required")
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
