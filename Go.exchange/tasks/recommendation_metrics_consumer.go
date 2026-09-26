package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	recommendationMetricsConsumerRetryDelay = 2 * time.Second
	recommendationMetricsBatchSize          = 500
	recommendationMetricsBatchWindow        = 50 * time.Millisecond
)

type recommendationMetricEvent struct {
	Envelope eventing.Envelope
	Payload  eventing.RecommendationBehaviorPayload
}

type recommendationMetricsMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type recommendationMetricRecordsApplyFunc func(context.Context, *gorm.DB, string, config.KafkaConfig, []recommendationMetricEvent) error

type recommendationMetricKey struct {
	MetricDate             time.Time
	Scene                  string
	RankerVersion          string
	RankerConfigHash       string
	StrategyID             string
	ExplorationOpportunity bool
	SelectionMode          string
	ExplorationReason      string
	Position               int
	PostID                 uint
}

type recommendationMetricDelta struct {
	ImpressionCount    int64
	ClickCount         int64
	QualifiedReadCount int64
	QuickBounceCount   int64
	NotInterestedCount int64
	FeedDwellCount     int64
	FeedVisibleTimeMS  int64
}

type recommendationMetricAggregate struct {
	Key   recommendationMetricKey
	Delta recommendationMetricDelta
}

func startRecommendationMetricsConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil ||
		strings.TrimSpace(config.AppConfig.Kafka.RecommendationEventsTopic) == "" ||
		strings.TrimSpace(config.AppConfig.Kafka.RecommendationMetricsGroupID) == "" {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelineRecommendationMetrics)
		defer PipelineStopped(PipelineRecommendationMetrics)
		for {
			runRecommendationMetricsConsumer(ctx)
			if ctx.Err() != nil {
				return
			}
			log.Printf("[RecommendationMetrics] consumer stopped; retrying in %s", recommendationMetricsConsumerRetryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(recommendationMetricsConsumerRetryDelay):
			}
		}
	}()
}

func runRecommendationMetricsConsumer(ctx context.Context) {
	kafkaConfig := config.AppConfig.Kafka
	reader, err := eventing.NewKafkaReader(
		kafkaConfig,
		kafkaConfig.RecommendationEventsTopic,
		kafkaConfig.RecommendationMetricsGroupID,
	)
	if err != nil {
		PipelineFailure(PipelineRecommendationMetrics, "kafka_reader_unavailable", 0)
		log.Printf("[RecommendationMetrics] create Kafka reader: %v", err)
		return
	}
	recommendationMetricsConsumers.Add(1)
	defer recommendationMetricsConsumers.Add(-1)
	defer reader.Close()
	publisher := eventingRawKafkaMessagePublisher{kafkaConfig: kafkaConfig}
	if err := consumeRecommendationMetricsMessages(ctx, reader, publisher, global.WorkerDb, kafkaConfig); err != nil && ctx.Err() == nil {
		PipelineFailure(PipelineRecommendationMetrics, "projection_failed", 0)
		log.Printf("[RecommendationMetrics] consumer stopped: %v", err)
	}
}

func consumeRecommendationMetricsMessages(
	ctx context.Context,
	reader recommendationMetricsMessageReader,
	publisher rawKafkaMessagePublisher,
	db *gorm.DB,
	kafkaConfig config.KafkaConfig,
) error {
	return consumeRecommendationMetricsMessagesWithApply(ctx, reader, publisher, db, kafkaConfig, defaultKafkaRetryPolicy, applyRecommendationMetricRecords)
}

func consumeRecommendationMetricsMessagesWithApply(
	ctx context.Context,
	reader recommendationMetricsMessageReader,
	publisher rawKafkaMessagePublisher,
	db *gorm.DB,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
	apply recommendationMetricRecordsApplyFunc,
) error {
	if ctx == nil {
		return errors.New("recommendation metrics consumer context is nil")
	}
	if reader == nil {
		return errors.New("recommendation metrics message reader is nil")
	}
	if apply == nil {
		return errors.New("recommendation metrics apply function is nil")
	}
	for {
		first, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				PipelineFailure(PipelineRecommendationMetrics, "kafka_fetch_failed", 0)
				log.Printf("[RecommendationMetrics] fetch Kafka message: %v", err)
			}
			return err
		}
		batch := collectRecommendationMetricsBatch(ctx, reader, first)
		records, err := classifyRecommendationMetricsBatch(ctx, publisher, kafkaConfig, batch)
		if err != nil {
			return fmt.Errorf("classify recommendation metrics batch of %d messages: %w", len(batch), err)
		}
		if len(records) > 0 {
			attempts := 0
			err = retryKafkaOperation(ctx, policy, func(attempt int, retryErr error) {
				metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCode(retryErr))
				log.Printf("[RecommendationMetrics] retry attempt=%d max_attempts=%d code=%s", attempt, policy.MaxAttempts, kafkaFailureCode(retryErr))
			}, func() error {
				attempts++
				applyErr := apply(ctx, db, strings.TrimSpace(kafkaConfig.RecommendationMetricsGroupID), kafkaConfig, records)
				return applyErr
			})
			if err != nil {
				metrics.RecordRecommendationTelemetryProjection("retryable_error")
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if kafkaFailureClassOf(err) == kafkaFailureRetryable && attempts >= policy.MaxAttempts {
					metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCode(err))
				}
				return fmt.Errorf("apply recommendation metrics batch of %d messages: %w", len(batch), err)
			}
			metrics.RecordRecommendationTelemetryProjection("applied")
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeBatchApplied, kafkaRecoveryCodeNone)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reader.CommitMessages(ctx, batch...); err != nil {
			if ctx.Err() == nil {
				PipelineFailure(PipelineRecommendationMetrics, "kafka_commit_failed", 0)
				log.Printf("[RecommendationMetrics] commit Kafka batch: %v", err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			commitErr := retryableKafkaError(kafkaFailureCodeKafkaCommit, err)
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(commitErr))
			return commitErr
		}
		backlog := int64(0)
		if statsReader, ok := reader.(interface{ Stats() kafka.ReaderStats }); ok {
			backlog = kafkaBacklog(statsReader)
		}
		PipelineCommit(PipelineRecommendationMetrics, time.Now().UTC(), backlog)
	}
}

func collectRecommendationMetricsBatch(ctx context.Context, reader recommendationMetricsMessageReader, first kafka.Message) []kafka.Message {
	batch := []kafka.Message{first}
	if len(batch) >= recommendationMetricsBatchSize {
		return batch
	}
	collectCtx, cancel := context.WithTimeout(ctx, recommendationMetricsBatchWindow)
	defer cancel()
	for len(batch) < recommendationMetricsBatchSize {
		message, err := reader.FetchMessage(collectCtx)
		if err != nil {
			if collectCtx.Err() != nil || ctx.Err() != nil {
				break
			}
			log.Printf("[RecommendationMetrics] collect Kafka message: %v", err)
			break
		}
		batch = append(batch, message)
	}
	return batch
}

func decodeRecommendationMetricEvent(message kafka.Message) (recommendationMetricEvent, error) {
	event, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, err)
	}
	if _, err := uuid.Parse(event.ID); err != nil {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("recommendation event id must be UUID"))
	}
	if event.SchemaVersion != eventing.RecommendationBehaviorSchemaVersion {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, fmt.Errorf("unsupported recommendation behavior schema version %d", event.SchemaVersion))
	}
	if !eventing.IsRecommendationEventType(event.Type) {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeUnsupportedEvent, fmt.Errorf("unsupported recommendation event type %q", event.Type))
	}
	if event.OccurredAt.IsZero() {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("recommendation event occurred_at is required"))
	}
	var payload eventing.RecommendationBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeDecodePayload, err)
	}
	if payload.UserID == 0 || payload.PostID == 0 || strings.TrimSpace(payload.RequestID) == "" ||
		strings.TrimSpace(payload.Scene) == "" || payload.Position <= 0 ||
		strings.TrimSpace(payload.RankerVersion) == "" || strings.TrimSpace(payload.RankerConfigHash) == "" ||
		strings.TrimSpace(payload.StrategyID) == "" || payload.ReceivedAt.IsZero() ||
		!validRecommendationMetricsPayload(event.Type, payload) {
		return recommendationMetricEvent{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("recommendation behavior payload failed validation"))
	}
	return recommendationMetricEvent{Envelope: event, Payload: payload}, nil
}

func classifyRecommendationMetricsBatch(
	ctx context.Context,
	publisher rawKafkaMessagePublisher,
	kafkaConfig config.KafkaConfig,
	messages []kafka.Message,
) ([]recommendationMetricEvent, error) {
	if ctx == nil {
		return nil, errors.New("recommendation metrics batch context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	valid := make([]recommendationMetricEvent, 0, len(messages))
	type permanentMessage struct {
		message kafka.Message
		err     error
	}
	permanent := make([]permanentMessage, 0)
	for _, message := range messages {
		record, err := decodeRecommendationMetricEvent(message)
		if err != nil {
			if kafkaFailureClassOf(err) != kafkaFailurePermanent {
				return nil, err
			}
			metrics.RecordRecommendationTelemetryProjection("invalid_payload")
			permanent = append(permanent, permanentMessage{message: message, err: err})
			continue
		}
		valid = append(valid, record)
	}
	for _, failure := range permanent {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		log.Printf("[RecommendationMetrics] route permanent message to DLQ topic=%s partition=%d offset=%d code=%s", failure.message.Topic, failure.message.Partition, failure.message.Offset, kafkaFailureCode(failure.err))
		if err := publishConsumerDLQ(ctx, publisher, kafkaConfig.ConsumerDLQTopic, kafkaConsumerRecommendationMetrics, failure.message, failure.err, 1); err != nil {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(err))
			return nil, err
		}
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCode(failure.err))
	}
	seenEventIDs := make(map[string]struct{}, len(valid))
	records := make([]recommendationMetricEvent, 0, len(valid))
	for _, record := range valid {
		if _, exists := seenEventIDs[record.Envelope.ID]; exists {
			metrics.RecordRecommendationTelemetryProjection("duplicate_in_batch")
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerRecommendationMetrics, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
			continue
		}
		seenEventIDs[record.Envelope.ID] = struct{}{}
		records = append(records, record)
	}
	return records, nil
}

func applyRecommendationMetricRecords(ctx context.Context, db *gorm.DB, consumerName string, _ config.KafkaConfig, records []recommendationMetricEvent) error {
	if ctx == nil {
		return errors.New("recommendation metrics apply context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	if db == nil {
		return retryableKafkaError(kafkaFailureCodeDatabaseUnavailable, errors.New("database is not initialized"))
	}
	consumerName = strings.TrimSpace(consumerName)
	if consumerName == "" {
		return errors.New("recommendation metrics consumer group is not configured")
	}

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		eventIDs := make([]string, 0, len(records))
		for _, record := range records {
			eventIDs = append(eventIDs, record.Envelope.ID)
		}
		firstDelivery, err := eventing.MarkInboxProcessedBatch(tx, consumerName, eventIDs)
		if err != nil {
			return err
		}
		metricAggregates := aggregateRecommendationMetrics(records, firstDelivery)
		if err := bulkUpsertRecommendationDailyMetrics(tx, metricAggregates); err != nil {
			return err
		}
		behaviorAggregates := aggregateRecommendationBehavior(records, firstDelivery)
		if err := bulkUpsertRecommendationBehavior(tx, behaviorAggregates); err != nil {
			return err
		}
		dirtyProfiles, err := recommendation.NewGormDirtyProfileRepository(tx)
		if err != nil {
			return err
		}
		return dirtyProfiles.InvalidateProfiles(ctx, recommendationMetricProfileInvalidationUsers(records, firstDelivery), "recommendation_feedback_projection", time.Now().UTC())
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err)
	}
	return nil
}

func recommendationMetricProfileInvalidationUsers(records []recommendationMetricEvent, firstDelivery map[string]struct{}) []uint {
	seen := make(map[uint]struct{})
	for _, record := range records {
		if _, ok := firstDelivery[record.Envelope.ID]; !ok || record.Payload.UserID == 0 {
			continue
		}
		if recommendationBehaviorAction(record) != "" {
			seen[record.Payload.UserID] = struct{}{}
		}
	}
	users := make([]uint, 0, len(seen))
	for userID := range seen {
		users = append(users, userID)
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	return users
}

func aggregateRecommendationMetrics(records []recommendationMetricEvent, firstDelivery map[string]struct{}) []recommendationMetricAggregate {
	byKey := make(map[recommendationMetricKey]recommendationMetricDelta, len(records))
	for _, record := range records {
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		occurredAt := record.Envelope.OccurredAt.UTC()
		key := recommendationMetricKey{
			MetricDate: time.Date(occurredAt.Year(), occurredAt.Month(), occurredAt.Day(), 0, 0, 0, 0, time.UTC),
			Scene:      record.Payload.Scene, RankerVersion: record.Payload.RankerVersion,
			RankerConfigHash: record.Payload.RankerConfigHash, StrategyID: record.Payload.StrategyID,
			ExplorationOpportunity: record.Payload.ExplorationOpportunity, SelectionMode: record.Payload.SelectionMode,
			ExplorationReason: record.Payload.ExplorationReason,
			Position:          record.Payload.Position, PostID: record.Payload.PostID,
		}
		delta := metricDeltaFor(record.Envelope.Type, record.Payload)
		current := byKey[key]
		current.ImpressionCount += delta.ImpressionCount
		current.ClickCount += delta.ClickCount
		current.QualifiedReadCount += delta.QualifiedReadCount
		current.QuickBounceCount += delta.QuickBounceCount
		current.NotInterestedCount += delta.NotInterestedCount
		current.FeedDwellCount += delta.FeedDwellCount
		current.FeedVisibleTimeMS += delta.FeedVisibleTimeMS
		byKey[key] = current
	}

	keys := make([]recommendationMetricKey, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		if !left.MetricDate.Equal(right.MetricDate) {
			return left.MetricDate.Before(right.MetricDate)
		}
		if left.Scene != right.Scene {
			return left.Scene < right.Scene
		}
		if left.RankerVersion != right.RankerVersion {
			return left.RankerVersion < right.RankerVersion
		}
		if left.RankerConfigHash != right.RankerConfigHash {
			return left.RankerConfigHash < right.RankerConfigHash
		}
		if left.StrategyID != right.StrategyID {
			return left.StrategyID < right.StrategyID
		}
		if left.ExplorationOpportunity != right.ExplorationOpportunity {
			return !left.ExplorationOpportunity
		}
		if left.SelectionMode != right.SelectionMode {
			return left.SelectionMode < right.SelectionMode
		}
		if left.ExplorationReason != right.ExplorationReason {
			return left.ExplorationReason < right.ExplorationReason
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.PostID < right.PostID
	})
	result := make([]recommendationMetricAggregate, 0, len(keys))
	for _, key := range keys {
		result = append(result, recommendationMetricAggregate{Key: key, Delta: byKey[key]})
	}
	return result
}

func metricDeltaFor(eventType string, payload eventing.RecommendationBehaviorPayload) recommendationMetricDelta {
	var delta recommendationMetricDelta
	switch eventType {
	case eventing.EventTypeRecommendationImpression:
		delta.ImpressionCount = 1
	case eventing.EventTypeRecommendationClick:
		delta.ClickCount = 1
	case eventing.EventTypeRecommendationReadEnd:
		if payload.ReadOutcome != nil {
			switch *payload.ReadOutcome {
			case "qualified":
				delta.QualifiedReadCount = 1
			case "quick_bounce":
				delta.QuickBounceCount = 1
			}
		}
	case eventing.EventTypeRecommendationNotInterested:
		delta.NotInterestedCount = 1
	case eventing.EventTypeRecommendationFeedDwell:
		delta.FeedDwellCount = 1
		if payload.FeedVisibleTimeMS != nil {
			delta.FeedVisibleTimeMS = *payload.FeedVisibleTimeMS
		}
	}
	return delta
}

type recommendationBehaviorKey struct {
	UserID uint
	PostID uint
	Action string
}

type recommendationBehaviorAggregate struct {
	Key        recommendationBehaviorKey
	Count      int64
	LastSeenAt time.Time
}

func aggregateRecommendationBehavior(records []recommendationMetricEvent, firstDelivery map[string]struct{}) []recommendationBehaviorAggregate {
	byKey := make(map[recommendationBehaviorKey]recommendationBehaviorAggregate, len(records))
	for _, record := range records {
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		action := recommendationBehaviorAction(record)
		if action == "" {
			continue
		}
		key := recommendationBehaviorKey{UserID: record.Payload.UserID, PostID: record.Payload.PostID, Action: action}
		current := byKey[key]
		current.Key = key
		current.Count++
		occurredAt := record.Envelope.OccurredAt.UTC()
		if current.LastSeenAt.IsZero() || occurredAt.After(current.LastSeenAt) {
			current.LastSeenAt = occurredAt
		}
		byKey[key] = current
	}
	keys := make([]recommendationBehaviorKey, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].UserID != keys[j].UserID {
			return keys[i].UserID < keys[j].UserID
		}
		if keys[i].PostID != keys[j].PostID {
			return keys[i].PostID < keys[j].PostID
		}
		return keys[i].Action < keys[j].Action
	})
	result := make([]recommendationBehaviorAggregate, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func recommendationBehaviorAction(record recommendationMetricEvent) string {
	switch record.Envelope.Type {
	case eventing.EventTypeRecommendationClick:
		return eventing.RecommendationBehaviorActionClick
	case eventing.EventTypeRecommendationReadEnd:
		if record.Payload.ReadOutcome == nil {
			return eventing.RecommendationBehaviorActionReadNeutral
		}
		switch *record.Payload.ReadOutcome {
		case "qualified":
			return eventing.RecommendationBehaviorActionReadQualified
		case "quick_bounce":
			return eventing.RecommendationBehaviorActionReadQuickBounce
		default:
			return eventing.RecommendationBehaviorActionReadNeutral
		}
	case eventing.EventTypeRecommendationNotInterested:
		return eventing.RecommendationBehaviorActionNotInterested
	default:
		return ""
	}
}

func bulkUpsertRecommendationBehavior(tx *gorm.DB, aggregates []recommendationBehaviorAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	rows := make([]models.PostBehavior, 0, len(aggregates))
	for _, aggregate := range aggregates {
		rows = append(rows, models.PostBehavior{
			Model:  gorm.Model{CreatedAt: updatedAt, UpdatedAt: updatedAt},
			UserID: aggregate.Key.UserID, PostID: aggregate.Key.PostID, Action: aggregate.Key.Action,
			Count: aggregate.Count, LastSeenAt: aggregate.LastSeenAt, Active: true,
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "post_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("post_behaviors.count + EXCLUDED.count"),
			"last_seen_at": gorm.Expr("GREATEST(post_behaviors.last_seen_at, EXCLUDED.last_seen_at)"),
			"active":       true,
			"updated_at":   gorm.Expr("EXCLUDED.updated_at"),
		}),
	}).Create(&rows).Error
}
func bulkUpsertRecommendationDailyMetrics(tx *gorm.DB, aggregates []recommendationMetricAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	metricsRows := make([]models.RecommendationDailyMetric, 0, len(aggregates))
	for _, aggregate := range aggregates {
		metricsRows = append(metricsRows, models.RecommendationDailyMetric{
			MetricDate: aggregate.Key.MetricDate, Scene: aggregate.Key.Scene,
			RankerVersion: aggregate.Key.RankerVersion, RankerConfigHash: aggregate.Key.RankerConfigHash,
			StrategyID: aggregate.Key.StrategyID, ExplorationOpportunity: aggregate.Key.ExplorationOpportunity,
			SelectionMode: aggregate.Key.SelectionMode, ExplorationReason: aggregate.Key.ExplorationReason,
			Position: aggregate.Key.Position, PostID: aggregate.Key.PostID,
			ImpressionCount: aggregate.Delta.ImpressionCount, ClickCount: aggregate.Delta.ClickCount,
			QualifiedReadCount: aggregate.Delta.QualifiedReadCount, QuickBounceCount: aggregate.Delta.QuickBounceCount,
			NotInterestedCount: aggregate.Delta.NotInterestedCount, FeedDwellCount: aggregate.Delta.FeedDwellCount,
			FeedVisibleTimeMS: aggregate.Delta.FeedVisibleTimeMS,
			UpdatedAt:         updatedAt,
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "metric_date"}, {Name: "scene"}, {Name: "ranker_version"},
			{Name: "ranker_config_hash"}, {Name: "strategy_id"}, {Name: "exploration_opportunity"},
			{Name: "selection_mode"}, {Name: "exploration_reason"}, {Name: "position"}, {Name: "post_id"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"impression_count":     gorm.Expr("recommendation_daily_metrics.impression_count + EXCLUDED.impression_count"),
			"click_count":          gorm.Expr("recommendation_daily_metrics.click_count + EXCLUDED.click_count"),
			"qualified_read_count": gorm.Expr("recommendation_daily_metrics.qualified_read_count + EXCLUDED.qualified_read_count"),
			"quick_bounce_count":   gorm.Expr("recommendation_daily_metrics.quick_bounce_count + EXCLUDED.quick_bounce_count"),
			"not_interested_count": gorm.Expr("recommendation_daily_metrics.not_interested_count + EXCLUDED.not_interested_count"),
			"feed_dwell_count":     gorm.Expr("recommendation_daily_metrics.feed_dwell_count + EXCLUDED.feed_dwell_count"),
			"feed_visible_time_ms": gorm.Expr("recommendation_daily_metrics.feed_visible_time_ms + EXCLUDED.feed_visible_time_ms"),
			"updated_at":           gorm.Expr("EXCLUDED.updated_at"),
		}),
	}).Create(&metricsRows).Error
}
