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
	"Go.exchange/embeddingstate"
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

type postEmbeddingWorkItem struct {
	Message     kafka.Message
	PostID      uint
	ContentHash string
	Text        string
}

type postEmbeddingWriteOutcome uint8

const (
	postEmbeddingWriteCommitted postEmbeddingWriteOutcome = iota
	postEmbeddingWriteStaleContent
	postEmbeddingWritePostMissing
)

var errPostEmbeddingSourceChanged = errors.New("post content changed during embedding generation")

type postEmbeddingStore interface {
	GetPost(context.Context, uint) (models.Post, error)
	GetEmbedding(context.Context, uint, string) (models.PostEmbedding, error)
	CommitEmbeddingIfCurrent(context.Context, models.PostEmbedding, time.Time) (postEmbeddingWriteOutcome, error)
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

func (s gormPostEmbeddingStore) GetEmbedding(ctx context.Context, postID uint, version string) (models.PostEmbedding, error) {
	var embedding models.PostEmbedding
	if s.db == nil {
		return embedding, errors.New("database is not initialized")
	}
	err := s.db.WithContext(ctx).Where("post_id = ? AND version = ?", postID, version).First(&embedding).Error
	return embedding, err
}

func lockEmbeddingServingVersion(tx *gorm.DB) (string, error) {
	if tx == nil {
		return "", errors.New("database transaction is not initialized")
	}
	var servingState models.EmbeddingServingState
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
		Select("id", "serving_version").
		Where("id = ?", embeddingstate.ServingStateID).
		Take(&servingState).Error; err != nil {
		return "", fmt.Errorf("lock embedding serving state: %w", err)
	}
	servingVersion := strings.TrimSpace(servingState.ServingVersion)
	if servingVersion == "" {
		return "", errors.New("embedding serving state version is blank")
	}
	return servingVersion, nil
}

// CommitEmbeddingIfCurrent validates the canonical post while holding its row
// lock, then commits the embedding and authoritative user fan-out in one
// transaction. The provider call happens before this method, so the post row
// is not locked during external network work.
func (s gormPostEmbeddingStore) CommitEmbeddingIfCurrent(ctx context.Context, embedding models.PostEmbedding, now time.Time) (postEmbeddingWriteOutcome, error) {
	if s.db == nil {
		return postEmbeddingWriteCommitted, errors.New("database is not initialized")
	}
	outcome := postEmbeddingWriteCommitted
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var currentPost models.Post
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "content").
			Where("id = ? AND deleted_at IS NULL", embedding.PostID).
			First(&currentPost).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			outcome = postEmbeddingWritePostMissing
			return nil
		}
		if err != nil {
			return err
		}
		if currentHash := embeddings.PostEmbeddingContentHash(currentPost.Content); currentHash != embedding.ContentHash {
			outcome = postEmbeddingWriteStaleContent
			return nil
		}
		servingVersion, err := lockEmbeddingServingVersion(tx)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "post_id"}, {Name: "version"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"model": embedding.Model, "dimensions": embedding.Dimensions,
				"embedding": embedding.Embedding, "content_hash": embedding.ContentHash, "updated_at": embedding.UpdatedAt,
			}),
		}).Create(&embedding).Error; err != nil {
			return err
		}
		if embedding.Version == servingVersion {
			var users []uint
			if err := tx.Raw(`
SELECT user_id FROM post_behaviors WHERE post_id = ?
UNION
SELECT user_id FROM post_reaction WHERE post_id = ?`, embedding.PostID, embedding.PostID).Scan(&users).Error; err != nil {
				return err
			}
			dirtyProfiles, err := recommendation.NewGormDirtyProfileRepository(tx)
			if err != nil {
				return err
			}
			if err := dirtyProfiles.InvalidateProfiles(ctx, users, "post_embedding_changed", now); err != nil {
				return err
			}
		}
		outcome = postEmbeddingWriteCommitted
		return nil
	})
	return outcome, err
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
		recovery := newPostEmbeddingRecoveryCoordinator(providerRetryRuntime{})
		for {
			err := runPostEmbeddingConsumer(ctx, recovery)
			if ctx.Err() != nil {
				metrics.SetEmbeddingProviderPause(false, "", 0)
				return
			}
			delay, decision, isProviderFailure := recovery.cooldownFor(err)
			if err != nil && !isProviderFailure {
				delay = postEmbeddingConsumerRestartDelay
			}
			if err == nil {
				delay = postEmbeddingConsumerRestartDelay
			}
			if isProviderFailure {
				log.Printf("[PostEmbedding] consumer cooldown action=%s reason=%s http_status=%d duration=%s", decision.Action, decision.Reason, decision.HTTPStatus, delay)
			}
			if waitErr := recovery.runtime.Sleep(ctx, delay); waitErr != nil {
				metrics.SetEmbeddingProviderPause(false, "", 0)
				return
			}
			if isProviderFailure {
				metrics.SetEmbeddingProviderPause(false, "", 0)
			}
		}
	}()
}

type postEmbeddingConsumerHooks struct {
	retryRuntime      providerRetryRuntime
	onProviderFailure func()
	onOffsetCommit    func()
}

func runPostEmbeddingConsumer(ctx context.Context, recovery *postEmbeddingRecoveryCoordinator) error {
	appConfig := config.AppConfig
	if appConfig == nil {
		decision := ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig}
		pipelineFailureWithKind(PipelinePostEmbedding, "embedding_config_invalid", 0, pipelineFailureConfiguration)
		metrics.RecordEmbeddingProviderFailure(providerReasonConfig)
		return &postEmbeddingProviderFailure{decision: decision, cause: errors.New("application config is not initialized")}
	}
	kafkaConfig := appConfig.Kafka
	embeddingConfig := appConfig.Embedding.Normalized()
	if err := embeddingConfig.ValidateBatchSettings(); err != nil {
		pipelineFailureWithKind(PipelinePostEmbedding, "embedding_config_invalid", 0, pipelineFailureConfiguration)
		metrics.RecordEmbeddingProviderFailure(providerReasonConfig)
		return &postEmbeddingProviderFailure{decision: ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig}, cause: err}
	}
	db := global.WorkerDb
	if db == nil {
		pipelineFailureWithKind(PipelinePostEmbedding, "database_unavailable", 0, pipelineFailureProcessing)
		return errors.New("database is not initialized")
	}
	buildVersion := strings.TrimSpace(config.BuildEmbeddingVersion())
	if buildVersion == "" {
		pipelineFailureWithKind(PipelinePostEmbedding, "embedding_config_invalid", 0, pipelineFailureConfiguration)
		metrics.RecordEmbeddingProviderFailure(providerReasonConfig)
		return &postEmbeddingProviderFailure{decision: ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig}, cause: errors.New("build embedding version is empty")}
	}
	if strings.TrimSpace(kafkaConfig.ConsumerDLQTopic) == "" {
		pipelineFailureWithKind(PipelinePostEmbedding, "embedding_config_invalid", 0, pipelineFailureConfiguration)
		return errors.New("consumer DLQ topic is required")
	}
	embedder, err := embeddings.NewOpenAICompatibleEmbedder(embeddingConfig)
	if err != nil {
		pipelineFailureWithKind(PipelinePostEmbedding, "embedding_provider_unavailable", 0, pipelineFailureConfiguration)
		metrics.RecordEmbeddingProviderFailure(providerReasonConfig)
		return &postEmbeddingProviderFailure{decision: ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig}, cause: err}
	}
	reader, clientID, err := eventing.NewKafkaReader(kafkaConfig, kafkaConfig.PostEmbeddingTopic, kafkaConfig.PostEmbeddingGroupID)
	if err != nil {
		pipelineFailureWithKind(PipelinePostEmbedding, "kafka_reader_unavailable", 0, pipelineFailureConfiguration)
		return err
	}
	trackedReader := trackKafkaConsumerReader(PipelinePostEmbedding, kafkaConfig.PostEmbeddingTopic, kafkaConfig.PostEmbeddingGroupID, clientID, reader)
	defer func() {
		if closeErr := trackedReader.Close(); closeErr != nil {
			log.Printf("[PostEmbedding] close Kafka reader: %v", closeErr)
		}
	}()
	publisher := eventingRawKafkaMessagePublisher{kafkaConfig: kafkaConfig}
	store := gormPostEmbeddingStore{db: db}
	hooks := postEmbeddingConsumerHooks{
		retryRuntime:      normalizedProviderRetryRuntime(recovery.runtime),
		onProviderFailure: recovery.noteProviderFailure,
		onOffsetCommit:    recovery.offsetCommitted,
	}
	if err := consumePostEmbeddingMessagesWithBatchPolicyAndHooks(
		ctx, trackedReader, publisher, embedder, store, buildVersion, kafkaConfig,
		embeddingConfig.BatchSize, time.Duration(embeddingConfig.BatchMaxWaitMs)*time.Millisecond, defaultKafkaRetryPolicy, hooks,
	); err != nil {
		if errors.Is(err, errPostEmbeddingSourceChanged) {
			log.Printf("[PostEmbedding] source changed during embedding; leaving Kafka message uncommitted for redelivery")
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[PostEmbedding] consume: %v", err)
		return err
	}
	return ctx.Err()
}

func consumePostEmbeddingMessages(
	ctx context.Context,
	reader postEmbeddingMessageReader,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
) error {
	return consumePostEmbeddingMessagesWithPolicy(ctx, reader, publisher, embedder, store, buildVersion, kafkaConfig, defaultKafkaRetryPolicy)
}

func consumePostEmbeddingMessagesWithPolicy(
	ctx context.Context,
	reader postEmbeddingMessageReader,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
) error {
	return consumePostEmbeddingMessagesWithBatchPolicy(ctx, reader, publisher, embedder, store, buildVersion, kafkaConfig, 1, 0, policy)
}

func consumePostEmbeddingMessagesWithBatchPolicy(
	ctx context.Context,
	reader postEmbeddingMessageReader,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	batchSize int,
	maxWait time.Duration,
	policy kafkaRetryPolicy,
) error {
	return consumePostEmbeddingMessagesWithBatchPolicyAndHooks(ctx, reader, publisher, embedder, store, buildVersion, kafkaConfig, batchSize, maxWait, policy, postEmbeddingConsumerHooks{})
}

func consumePostEmbeddingMessagesWithBatchPolicyAndHooks(
	ctx context.Context,
	reader postEmbeddingMessageReader,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	batchSize int,
	maxWait time.Duration,
	policy kafkaRetryPolicy,
	hooks postEmbeddingConsumerHooks,
) error {
	if ctx == nil {
		return errors.New("post embedding consumer context is nil")
	}
	if reader == nil {
		return errors.New("post embedding message reader is nil")
	}
	if embedder == nil {
		return errors.New("post embedding provider is nil")
	}
	if store == nil {
		return errors.New("post embedding store is nil")
	}
	if batchSize < 1 || batchSize > config.MaxEmbeddingBatchSize {
		return fmt.Errorf("post embedding batch size must be between 1 and %d", config.MaxEmbeddingBatchSize)
	}
	if batchSize > 1 && (maxWait <= 0 || maxWait > time.Duration(config.MaxEmbeddingBatchMaxWaitMs)*time.Millisecond) {
		return fmt.Errorf("post embedding batch max wait must be between 1ms and %dms", config.MaxEmbeddingBatchMaxWaitMs)
	}
	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "" {
		return errors.New("build embedding version is required")
	}
	kafkaConfig.ConsumerDLQTopic = strings.TrimSpace(kafkaConfig.ConsumerDLQTopic)
	if kafkaConfig.ConsumerDLQTopic == "" {
		return errors.New("consumer DLQ topic is required")
	}
	for {
		started := time.Now()
		batch, err := fetchPostEmbeddingBatch(ctx, reader, batchSize, maxWait)
		if err != nil {
			metrics.RecordPostEmbeddingBatchFailure()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			pipelineFailureWithKind(PipelinePostEmbedding, "kafka_fetch_failed", 0, kafkaFetchFailureKind(err))
			return err
		}
		metrics.ObservePostEmbeddingKafkaBatchSize(len(batch))
		processErr := processPostEmbeddingBatchWithHooks(ctx, batch, publisher, embedder, store, buildVersion, kafkaConfig, policy, hooks)
		if processErr != nil {
			metrics.RecordPostEmbeddingBatchFailure()
			metrics.ObservePostEmbeddingProcessingDuration(time.Since(started))
			if ctx.Err() == nil && !errors.Is(processErr, errPostEmbeddingSourceChanged) {
				pipelineFailureWithKind(PipelinePostEmbedding, "projection_failed", 0, pipelineFailureProcessing)
			}
			return processErr
		}
		if err := ctx.Err(); err != nil {
			metrics.ObservePostEmbeddingProcessingDuration(time.Since(started))
			return err
		}
		if err := reader.CommitMessages(ctx, batch...); err != nil {
			metrics.ObservePostEmbeddingProcessingDuration(time.Since(started))
			metrics.RecordPostEmbeddingBatchFailure()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			pipelineFailureWithKind(PipelinePostEmbedding, "kafka_commit_failed", 0, pipelineFailureCommit)
			metrics.RecordPostEmbeddingFailure("kafka_commit")
			commitErr := retryableKafkaError(kafkaFailureCodeKafkaCommit, err)
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(commitErr))
			return commitErr
		}
		metrics.ObservePostEmbeddingProcessingDuration(time.Since(started))
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeBatchApplied, kafkaRecoveryCodeNone)
		PipelineCommit(PipelinePostEmbedding, time.Now().UTC(), 0)
		if hooks.onOffsetCommit != nil {
			hooks.onOffsetCommit()
		}
	}
}

func fetchPostEmbeddingBatch(
	ctx context.Context,
	reader postEmbeddingMessageReader,
	batchSize int,
	maxWait time.Duration,
) ([]kafka.Message, error) {
	if ctx == nil {
		return nil, errors.New("post embedding batch context is nil")
	}
	if reader == nil {
		return nil, errors.New("post embedding message reader is nil")
	}
	if batchSize < 1 || batchSize > config.MaxEmbeddingBatchSize {
		return nil, fmt.Errorf("post embedding batch size must be between 1 and %d", config.MaxEmbeddingBatchSize)
	}
	if batchSize > 1 && (maxWait <= 0 || maxWait > time.Duration(config.MaxEmbeddingBatchMaxWaitMs)*time.Millisecond) {
		return nil, fmt.Errorf("post embedding batch max wait must be between 1ms and %dms", config.MaxEmbeddingBatchMaxWaitMs)
	}
	first, err := reader.FetchMessage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	batch := []kafka.Message{first}
	if batchSize == 1 {
		return batch, nil
	}
	collectStarted := time.Now()
	defer func() { metrics.ObservePostEmbeddingBatchWait(time.Since(collectStarted)) }()
	deadline := collectStarted.Add(maxWait)
	for len(batch) < batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return batch, nil
		}
		fetchCtx, cancel := context.WithDeadline(ctx, deadline)
		message, fetchErr := reader.FetchMessage(fetchCtx)
		fetchDeadlineExceeded := errors.Is(fetchCtx.Err(), context.DeadlineExceeded)
		cancel()
		if fetchErr == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			batch = append(batch, message)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fetchDeadlineExceeded && errors.Is(fetchErr, context.DeadlineExceeded) {
			return batch, nil
		}
		return nil, fetchErr
	}
	return batch, nil
}

func processPostEmbeddingMessage(
	ctx context.Context,
	message kafka.Message,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
) error {
	return processPostEmbeddingBatch(ctx, []kafka.Message{message}, publisher, embedder, store, buildVersion, kafkaConfig, policy)
}

func processPostEmbeddingBatch(
	ctx context.Context,
	messages []kafka.Message,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
) error {
	return processPostEmbeddingBatchWithHooks(ctx, messages, publisher, embedder, store, buildVersion, kafkaConfig, policy, postEmbeddingConsumerHooks{})
}

func processPostEmbeddingBatchWithHooks(
	ctx context.Context,
	messages []kafka.Message,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
	hooks postEmbeddingConsumerHooks,
) error {
	if ctx == nil {
		return errors.New("post embedding processing context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return errors.New("post embedding store is nil")
	}
	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "" {
		return errors.New("build embedding version is required")
	}
	workItems := make([]postEmbeddingWorkItem, 0, len(messages))
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		postID, err := decodePostEmbeddingMessage(message)
		if err != nil {
			if kafkaFailureClassOf(err) != kafkaFailurePermanent {
				return err
			}
			log.Printf("[PostEmbedding] route permanent message to DLQ topic=%s partition=%d offset=%d code=%s", message.Topic, message.Partition, message.Offset, kafkaFailureCode(err))
			metrics.RecordPostEmbeddingFailure("decode")
			metrics.RecordPostEmbeddingEvent("invalid_event")
			if err := publishPostEmbeddingDLQ(ctx, publisher, kafkaConfig, message, err); err != nil {
				return err
			}
			continue
		}
		post, contentHash, noop, err := loadPostEmbeddingSource(ctx, store, postID, buildVersion, policy)
		if err != nil {
			return err
		}
		if noop {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
			continue
		}
		workItems = append(workItems, postEmbeddingWorkItem{
			Message: message, PostID: postID, ContentHash: contentHash,
			Text: embeddings.BuildPostEmbeddingText(post.Content),
		})
	}
	if len(workItems) == 0 {
		return nil
	}
	return processPostEmbeddingWorkItemsWithHooks(ctx, workItems, publisher, embedder, store, buildVersion, kafkaConfig, policy, hooks)
}

func processPostEmbeddingWorkItems(
	ctx context.Context,
	workItems []postEmbeddingWorkItem,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
) error {
	return processPostEmbeddingWorkItemsWithHooks(ctx, workItems, publisher, embedder, store, buildVersion, kafkaConfig, policy, postEmbeddingConsumerHooks{})
}

func processPostEmbeddingWorkItemsWithHooks(
	ctx context.Context,
	workItems []postEmbeddingWorkItem,
	publisher rawKafkaMessagePublisher,
	embedder embeddings.Embedder,
	store postEmbeddingStore,
	buildVersion string,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
	hooks postEmbeddingConsumerHooks,
) error {
	texts := make([]string, len(workItems))
	for index, item := range workItems {
		texts[index] = item.Text
	}
	result, err := generatePostEmbeddingTextsWithRuntime(ctx, embedder, texts, policy, hooks.retryRuntime, hooks.onProviderFailure)
	if err != nil {
		decision, providerFailure := providerFailureDecision(err)
		if !providerFailure {
			return err
		}
		if decision.Action != ProviderActionSplitBatch || len(workItems) < 2 {
			return err
		}
		metrics.RecordPostEmbeddingBatchSplit()
		middle := len(workItems) / 2
		if err := processPostEmbeddingWorkItemsWithHooks(ctx, workItems[:middle], publisher, embedder, store, buildVersion, kafkaConfig, policy, hooks); err != nil {
			return err
		}
		return processPostEmbeddingWorkItemsWithHooks(ctx, workItems[middle:], publisher, embedder, store, buildVersion, kafkaConfig, policy, hooks)
	}

	modelName := strings.TrimSpace(result.Model)
	now := time.Now().UTC()
	for index, item := range workItems {
		if err := ctx.Err(); err != nil {
			return err
		}
		vector := result.Vectors[index]
		embedding := models.PostEmbedding{
			PostID: item.PostID, Version: buildVersion, Model: modelName,
			Dimensions: len(vector), Embedding: pgvector.NewVector(vector), ContentHash: item.ContentHash,
			CreatedAt: now, UpdatedAt: now,
		}
		outcome, err := commitPostEmbedding(ctx, store, embedding, now, policy)
		if err != nil {
			return err
		}
		switch outcome {
		case postEmbeddingWriteCommitted:
			metrics.RecordPostEmbeddingEvent("generated")
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeMessageApplied, kafkaRecoveryCodeNone)
		case postEmbeddingWriteStaleContent:
			metrics.RecordPostEmbeddingEvent("stale_content_discarded")
			staleErr := retryableKafkaError(kafkaFailureCodeSourceChanged, errPostEmbeddingSourceChanged)
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(staleErr))
			return staleErr
		case postEmbeddingWritePostMissing:
			metrics.RecordPostEmbeddingEvent("post_missing_after_embed")
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
		default:
			stateErr := retryableKafkaError(kafkaFailureCodeInternalState, fmt.Errorf("unknown post embedding write outcome %d", outcome))
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(stateErr))
			return stateErr
		}
	}
	return nil
}

func loadPostEmbeddingSource(
	ctx context.Context,
	store postEmbeddingStore,
	postID uint,
	buildVersion string,
	policy kafkaRetryPolicy,
) (models.Post, string, bool, error) {
	var post models.Post
	var postErr error
	err := retryPostEmbeddingStage(ctx, policy, func() error {
		post, postErr = store.GetPost(ctx, postID)
		if errors.Is(postErr, gorm.ErrRecordNotFound) {
			return nil
		}
		if postErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			metrics.RecordPostEmbeddingFailure("db_read")
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, postErr)
		}
		return nil
	})
	if err != nil {
		return models.Post{}, "", false, err
	}
	if errors.Is(postErr, gorm.ErrRecordNotFound) {
		metrics.RecordPostEmbeddingEvent("post_missing")
		return models.Post{}, "", true, nil
	}

	contentHash := embeddings.PostEmbeddingContentHash(post.Content)
	var existing models.PostEmbedding
	var embeddingErr error
	err = retryPostEmbeddingStage(ctx, policy, func() error {
		existing, embeddingErr = store.GetEmbedding(ctx, postID, buildVersion)
		if errors.Is(embeddingErr, gorm.ErrRecordNotFound) {
			return nil
		}
		if embeddingErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			metrics.RecordPostEmbeddingFailure("db_read")
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, embeddingErr)
		}
		return nil
	})
	if err != nil {
		return models.Post{}, "", false, err
	}
	if embeddingErr == nil && existing.ContentHash == contentHash {
		metrics.RecordPostEmbeddingEvent("up_to_date")
		return models.Post{}, "", true, nil
	}
	return post, contentHash, false, nil
}

func generatePostEmbeddingTexts(ctx context.Context, embedder embeddings.Embedder, texts []string, policy kafkaRetryPolicy) (embeddings.EmbedResult, error) {
	return generatePostEmbeddingTextsWithRuntime(ctx, embedder, texts, policy, providerRetryRuntime{}, nil)
}

func generatePostEmbeddingTextsWithRuntime(
	ctx context.Context,
	embedder embeddings.Embedder,
	texts []string,
	policy kafkaRetryPolicy,
	runtime providerRetryRuntime,
	onProviderFailure func(),
) (embeddings.EmbedResult, error) {
	if ctx == nil {
		return embeddings.EmbedResult{}, errors.New("post embedding provider context is nil")
	}
	runtime = normalizedProviderRetryRuntime(runtime)
	policy = normalizedKafkaRetryPolicy(policy)
	var lastFailure *postEmbeddingProviderFailure
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return embeddings.EmbedResult{}, err
		}
		generated, err := embedder.Embed(ctx, texts)
		if err == nil {
			if contractErr := validatePostEmbeddingBatchResult(generated, len(texts)); contractErr != nil {
				err = contractErr
				metrics.RecordEmbeddingProviderFailure(providerReasonContract)
				decision := ProviderDecision{Reason: providerReasonContract}
				if len(texts) > 1 {
					decision.Action = ProviderActionSplitBatch
				} else {
					decision.Action = ProviderActionPause
				}
				lastFailure = &postEmbeddingProviderFailure{decision: decision, cause: err}
			} else {
				metrics.RecordPostEmbeddingAPIRequest("success", len(texts))
				generated.Model = strings.TrimSpace(generated.Model)
				return generated, nil
			}
		} else {
			if errors.Is(err, context.Canceled) {
				return embeddings.EmbedResult{}, err
			}
			if ctx.Err() != nil {
				return embeddings.EmbedResult{}, ctx.Err()
			}
			decision := classifyPostEmbeddingProviderError(err, len(texts))
			lastFailure = &postEmbeddingProviderFailure{decision: decision, cause: err}
			metrics.RecordEmbeddingProviderFailure(decision.Reason)
			if decision.RetryAfterProvided && !decision.RetryAfterValid {
				metrics.RecordEmbeddingProviderFailure(providerReasonRetryAfterInvalid)
				logInvalidRetryAfter(decision.HTTPStatus)
			}
			if decision.RetryAfterValid && decision.RetryAfter > postEmbeddingLongRetryAfterAlert {
				metrics.RecordEmbeddingProviderFailure(providerReasonRetryAfterLong)
				log.Printf("[PostEmbedding] provider Retry-After is unusually long; honoring the supplied delay status=%d", decision.HTTPStatus)
			}
		}
		metrics.RecordPostEmbeddingAPIRequest("error", len(texts))
		metrics.RecordPostEmbeddingFailure("provider")
		if onProviderFailure != nil {
			onProviderFailure()
		}
		if lastFailure.decision.Action != ProviderActionRetry {
			return embeddings.EmbedResult{}, lastFailure
		}
		if lastFailure.decision.RetryAfterValid && lastFailure.decision.RetryAfter > policy.MaxBackoff {
			return embeddings.EmbedResult{}, lastFailure
		}
		if attempt == policy.MaxAttempts {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable)
			return embeddings.EmbedResult{}, retryableKafkaError(kafkaFailureCodeProviderRetryable, lastFailure)
		}
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable)
		backoff := providerRetryBackoff(policy, attempt, runtime.Jitter)
		if lastFailure.decision.RetryAfterValid && lastFailure.decision.RetryAfter > backoff {
			backoff = lastFailure.decision.RetryAfter
		}
		if err := runtime.Sleep(ctx, backoff); err != nil {
			return embeddings.EmbedResult{}, err
		}
	}
	return embeddings.EmbedResult{}, retryableKafkaError(kafkaFailureCodeProviderRetryable, lastFailure)
}

func providerRetryBackoff(policy kafkaRetryPolicy, attempt int, jitter func(time.Duration) time.Duration) time.Duration {
	delay := policy.InitialBackoff
	if delay <= 0 {
		delay = defaultKafkaRetryPolicy.InitialBackoff
	}
	for step := 1; step < attempt && delay < policy.MaxBackoff; step++ {
		if delay > policy.MaxBackoff/2 {
			delay = policy.MaxBackoff
			break
		}
		delay *= 2
	}
	if policy.MaxBackoff > 0 && delay > policy.MaxBackoff {
		delay = policy.MaxBackoff
	}
	if jitter != nil {
		delay = jitter(delay)
	}
	if policy.MaxBackoff > 0 && delay > policy.MaxBackoff {
		delay = policy.MaxBackoff
	}
	return delay
}

func normalizedKafkaRetryPolicy(policy kafkaRetryPolicy) kafkaRetryPolicy {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = defaultKafkaRetryPolicy.MaxAttempts
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaultKafkaRetryPolicy.InitialBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = defaultKafkaRetryPolicy.MaxBackoff
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}
	return policy
}

func validatePostEmbeddingBatchResult(result embeddings.EmbedResult, expectedVectors int) error {
	if expectedVectors < 1 {
		return errors.New("embedding request must contain at least one text")
	}
	if len(result.Vectors) != expectedVectors {
		return fmt.Errorf("embedding provider returned %d vectors for %d texts", len(result.Vectors), expectedVectors)
	}
	if strings.TrimSpace(result.Model) == "" {
		return errors.New("embedding provider returned an empty model")
	}
	dimensions := 0
	for index, vector := range result.Vectors {
		if !validPostEmbeddingVector(vector) {
			return fmt.Errorf("embedding provider returned an empty or non-finite vector at index %d", index)
		}
		if dimensions == 0 {
			dimensions = len(vector)
		}
		if len(vector) != dimensions {
			return errors.New("embedding provider returned inconsistent dimensions")
		}
	}
	return nil
}

func commitPostEmbedding(ctx context.Context, store postEmbeddingStore, embedding models.PostEmbedding, now time.Time, policy kafkaRetryPolicy) (postEmbeddingWriteOutcome, error) {
	var outcome postEmbeddingWriteOutcome
	err := retryPostEmbeddingStage(ctx, policy, func() error {
		var err error
		outcome, err = store.CommitEmbeddingIfCurrent(ctx, embedding, now)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			metrics.RecordPostEmbeddingFailure("db_upsert")
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err)
		}
		return nil
	})
	return outcome, err
}

func retryPostEmbeddingStage(ctx context.Context, policy kafkaRetryPolicy, operation func() error) error {
	attempts := 0
	err := retryKafkaOperation(ctx, policy, func(attempt int, retryErr error) {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCode(retryErr))
		log.Printf("[PostEmbedding] retry attempt=%d max_attempts=%d code=%s", attempt, policy.MaxAttempts, kafkaFailureCode(retryErr))
	}, func() error {
		attempts++
		return operation()
	})
	if err != nil && ctx.Err() == nil && kafkaFailureClassOf(err) == kafkaFailureRetryable && attempts >= policy.MaxAttempts {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCode(err))
	}
	return err
}

func publishPostEmbeddingDLQ(ctx context.Context, publisher rawKafkaMessagePublisher, kafkaConfig config.KafkaConfig, message kafka.Message, failure error) error {
	if err := publishConsumerDLQ(ctx, publisher, kafkaConfig.ConsumerDLQTopic, kafkaConsumerPostEmbedding, message, failure, 1); err != nil {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(err))
		return err
	}
	metrics.RecordKafkaConsumerRecovery(kafkaConsumerPostEmbedding, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCode(failure))
	return nil
}

func decodePostEmbeddingMessage(message kafka.Message) (uint, error) {
	event, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return 0, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, err)
	}
	if _, err := uuid.Parse(event.ID); err != nil {
		return 0, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post embedding event id must be a UUID"))
	}
	if event.Type != eventing.EventTypePostEmbeddingRequested {
		return 0, permanentKafkaError(kafkaFailureCodeUnsupportedEvent, fmt.Errorf("unexpected post embedding event type %q", event.Type))
	}
	if event.SchemaVersion != 1 {
		return 0, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, fmt.Errorf("unsupported post embedding schema version %d", event.SchemaVersion))
	}
	if event.AggregateType != "post" {
		return 0, permanentKafkaError(kafkaFailureCodeInvalidPayload, fmt.Errorf("unexpected post embedding aggregate type %q", event.AggregateType))
	}
	if event.OccurredAt.IsZero() {
		return 0, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post embedding occurred_at is required"))
	}
	var payload eventing.PostEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return 0, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode post embedding payload: %w", err))
	}
	if payload.PostID == 0 {
		return 0, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post embedding payload post_id is required"))
	}
	if event.AggregateID != strconv.FormatUint(uint64(payload.PostID), 10) {
		return 0, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post embedding aggregate_id does not match payload post_id"))
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
