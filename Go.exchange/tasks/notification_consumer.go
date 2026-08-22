package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	notificationConsumerRetryDelay = 2 * time.Second
	notificationBatchSize          = 500
	notificationBatchWindow        = 50 * time.Millisecond
)

var notificationConsumers atomicCounter

type atomicCounter struct{ value int32 }

func (c *atomicCounter) Add(delta int32) { atomic.AddInt32(&c.value, delta) }
func (c *atomicCounter) Load() int32     { return atomic.LoadInt32(&c.value) }

type notificationMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type notificationLagReader interface {
	Stats() kafka.ReaderStats
}

type notificationActivityRecord struct {
	Message   kafka.Message
	Envelope  eventing.Envelope
	Candidate *models.Notification
}

func notificationConsumerConfigured() bool {
	return config.AppConfig != nil && strings.TrimSpace(config.AppConfig.Kafka.ActivityEventsTopic) != "" && strings.TrimSpace(config.AppConfig.Kafka.NotificationGroupID) != ""
}

func startNotificationProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if !notificationConsumerConfigured() {
		return
	}
	for workerID := 1; workerID <= config.NotificationProjectionConsumers(); workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				runNotificationProjectionConsumer(ctx)
				if ctx.Err() != nil {
					return
				}
				log.Printf("[NotificationProjection:%d] consumer stopped; retrying in %s", id, notificationConsumerRetryDelay)
				select {
				case <-ctx.Done():
					return
				case <-time.After(notificationConsumerRetryDelay):
				}
			}
		}(workerID)
	}
}

func runNotificationProjectionConsumer(ctx context.Context) {
	reader, err := eventing.NewKafkaReader(config.AppConfig.Kafka, config.AppConfig.Kafka.ActivityEventsTopic, config.AppConfig.Kafka.NotificationGroupID)
	if err != nil {
		log.Printf("[NotificationProjection] create Kafka reader: %v", err)
		return
	}
	publisher := rawNotificationPublisher{config: config.AppConfig.Kafka}
	notificationConsumers.Add(1)
	defer notificationConsumers.Add(-1)
	defer reader.Close()
	if err := consumeNotificationMessages(ctx, reader, publisher); err != nil && ctx.Err() == nil {
		log.Printf("[NotificationProjection] consumer stopped: %v", err)
	}
}

func consumeNotificationMessages(ctx context.Context, reader notificationMessageReader, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}) error {
	for {
		first, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		batch := collectNotificationBatch(ctx, reader, first)
		if err := processNotificationBatch(ctx, batch, publisher); err != nil {
			return err
		}
		if err := reader.CommitMessages(ctx, batch...); err != nil {
			return err
		}
		if statsReader, ok := reader.(notificationLagReader); ok {
			lag := statsReader.Stats().Lag
			if lag < 0 {
				lag = 0
			}
			metrics.SetNotificationConsumerLag(float64(lag))
		}
	}
}

func collectNotificationBatch(ctx context.Context, reader notificationMessageReader, first kafka.Message) []kafka.Message {
	batch := []kafka.Message{first}
	if len(batch) >= notificationBatchSize {
		return batch
	}
	collectCtx, cancel := context.WithTimeout(ctx, notificationBatchWindow)
	defer cancel()
	for len(batch) < notificationBatchSize {
		message, err := reader.FetchMessage(collectCtx)
		if err != nil {
			break
		}
		batch = append(batch, message)
	}
	return batch
}

type rawNotificationPublisher struct{ config config.KafkaConfig }

func (p rawNotificationPublisher) PublishRaw(ctx context.Context, topic string, messages ...kafka.Message) error {
	return eventing.PublishRawMessages(ctx, p.config, topic, messages...)
}

func processNotificationBatch(ctx context.Context, messages []kafka.Message, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}) error {
	started := time.Now()
	records := make([]notificationActivityRecord, 0, len(messages))
	for _, message := range messages {
		record, err := decodeNotificationActivity(message)
		if err != nil {
			if dlqErr := publishNotificationDLQ(ctx, publisher, message, err); dlqErr != nil {
				metrics.RecordNotificationProjectionFailure("dlq")
				return fmt.Errorf("publish notification DLQ: %w", dlqErr)
			}
			metrics.RecordNotificationProjectionDLQ()
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}
	if err := applyNotificationRecords(records); err != nil {
		metrics.RecordNotificationProjectionFailure("database")
		return err
	}
	metrics.ObserveNotificationProjectionLatency(time.Since(started))
	return nil
}

func decodeNotificationActivity(message kafka.Message) (notificationActivityRecord, error) {
	envelope, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return notificationActivityRecord{}, err
	}
	if _, err := uuid.Parse(strings.TrimSpace(envelope.ID)); err != nil {
		return notificationActivityRecord{}, errors.New("notification activity id must be a UUID")
	}
	if envelope.SchemaVersion < 1 || strings.TrimSpace(envelope.AggregateType) == "" || strings.TrimSpace(envelope.AggregateID) == "" || envelope.OccurredAt.IsZero() {
		return notificationActivityRecord{}, errors.New("notification activity envelope is missing required fields")
	}
	var payloadObject map[string]json.RawMessage
	if len(envelope.Payload) == 0 || json.Unmarshal(envelope.Payload, &payloadObject) != nil || payloadObject == nil {
		return notificationActivityRecord{}, errors.New("notification activity payload must be a JSON object")
	}
	record := notificationActivityRecord{Message: message, Envelope: envelope}
	switch envelope.Type {
	case eventing.EventTypeArticleReactionApplied:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "article_reaction" {
			return notificationActivityRecord{}, errors.New("unsupported article reaction activity schema")
		}
		var payload eventing.ArticleReactionAppliedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, fmt.Errorf("decode article reaction activity payload: %w", err)
		}
		if payload.ActorID == 0 || payload.ArticleID == 0 || payload.ArticleAuthorID == 0 || payload.ReactionVersion <= 0 || payload.StateChangedAt.IsZero() || !payload.StateChangedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != fmt.Sprintf("%d:%d", payload.ActorID, payload.ArticleID) {
			return notificationActivityRecord{}, errors.New("invalid article reaction activity payload")
		}
		if !payload.Liked || payload.ActorID == payload.ArticleAuthorID {
			return record, nil
		}
		articleID := payload.ArticleID
		record.Candidate = &models.Notification{
			RecipientID: payload.ArticleAuthorID, ActorID: payload.ActorID, Type: models.NotificationTypePostLiked,
			ArticleID: &articleID, DedupeKey: fmt.Sprintf("post_like:%d:%d", payload.ActorID, payload.ArticleID),
			SourceVersion: payload.ReactionVersion, ActivityAt: payload.StateChangedAt,
		}
	case eventing.EventTypeCommentCreated:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "comment" {
			return notificationActivityRecord{}, errors.New("unsupported comment activity schema")
		}
		var payload eventing.CommentCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, fmt.Errorf("decode comment activity payload: %w", err)
		}
		if payload.CommentID == 0 || payload.ArticleID == 0 || payload.ActorID == 0 || payload.ArticleAuthorID == 0 || payload.CreatedAt.IsZero() || !payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != strconv.FormatUint(uint64(payload.CommentID), 10) {
			return notificationActivityRecord{}, errors.New("invalid comment activity payload")
		}
		if payload.ActorID == payload.ArticleAuthorID {
			return record, nil
		}
		articleID, commentID := payload.ArticleID, payload.CommentID
		record.Candidate = &models.Notification{
			RecipientID: payload.ArticleAuthorID, ActorID: payload.ActorID, Type: models.NotificationTypePostReplied,
			ArticleID: &articleID, CommentID: &commentID, DedupeKey: fmt.Sprintf("post_reply:%d", payload.CommentID),
			ActivityAt: payload.CreatedAt,
		}
	case eventing.EventTypeUserFollowCreated:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "user_follow" {
			return notificationActivityRecord{}, errors.New("unsupported follow activity schema")
		}
		var payload eventing.UserFollowCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, fmt.Errorf("decode follow activity payload: %w", err)
		}
		if payload.FollowID == 0 || payload.FollowerID == 0 || payload.FollowingID == 0 || payload.FollowerID == payload.FollowingID || payload.CreatedAt.IsZero() || !payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != strconv.FormatUint(uint64(payload.FollowID), 10) {
			return notificationActivityRecord{}, errors.New("invalid follow activity payload")
		}
		if payload.FollowerID == payload.FollowingID {
			return record, nil
		}
		record.Candidate = &models.Notification{
			RecipientID: payload.FollowingID, ActorID: payload.FollowerID, Type: models.NotificationTypeUserFollowed,
			DedupeKey:     fmt.Sprintf("user_followed:%d:%d", payload.FollowerID, payload.FollowingID),
			SourceVersion: int64(payload.FollowID), ActivityAt: payload.CreatedAt,
		}
	default:
		// A well-formed unknown event is a forward-compatible no-op.
	}
	return record, nil
}

func applyNotificationBatch(messages []kafka.Message) error {
	records := make([]notificationActivityRecord, 0, len(messages))
	for _, message := range messages {
		record, err := decodeNotificationActivity(message)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	return applyNotificationRecords(records)
}

func applyNotificationRecords(records []notificationActivityRecord) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	if config.AppConfig == nil || strings.TrimSpace(config.AppConfig.Kafka.NotificationGroupID) == "" {
		return errors.New("notification consumer group is not configured")
	}
	return global.Db.Transaction(func(tx *gorm.DB) error {
		eventIDs := make([]string, 0, len(records))
		for _, record := range records {
			eventIDs = append(eventIDs, record.Envelope.ID)
		}
		firstDelivery, err := eventing.MarkInboxProcessedBatch(tx, config.AppConfig.Kafka.NotificationGroupID, eventIDs)
		if err != nil {
			return err
		}
		candidates := make([]models.Notification, 0, len(records))
		seenDedupe := make(map[string]int)
		for _, record := range records {
			if _, ok := firstDelivery[record.Envelope.ID]; !ok || record.Candidate == nil {
				continue
			}
			candidate := *record.Candidate
			if index, exists := seenDedupe[candidate.DedupeKey]; exists {
				if candidate.SourceVersion > candidates[index].SourceVersion {
					candidates[index] = candidate
				}
				continue
			}
			seenDedupe[candidate.DedupeKey] = len(candidates)
			candidates = append(candidates, candidate)
		}
		if len(candidates) == 0 {
			return nil
		}
		candidates, err = filterNotificationCandidates(tx, candidates)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		return upsertNotificationCandidates(tx, candidates)
	})
}

func filterNotificationCandidates(tx *gorm.DB, candidates []models.Notification) ([]models.Notification, error) {
	recipientIDs := make([]uint, 0, len(candidates))
	actorIDs := make([]uint, 0, len(candidates))
	articleIDs := make([]uint, 0, len(candidates))
	commentIDs := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		recipientIDs = append(recipientIDs, candidate.RecipientID)
		actorIDs = append(actorIDs, candidate.ActorID)
		if candidate.ArticleID != nil {
			articleIDs = append(articleIDs, *candidate.ArticleID)
		}
		if candidate.CommentID != nil {
			commentIDs = append(commentIDs, *candidate.CommentID)
		}
	}
	activeUsers := make(map[uint]struct{})
	var users []models.User
	if err := tx.Select("id").Where("id IN ? AND deleted_at IS NULL", uniqueUintIDs(append(recipientIDs, actorIDs...))).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		activeUsers[user.ID] = struct{}{}
	}
	articles := make(map[uint]struct{})
	var articleRows []struct {
		ID uint `gorm:"column:id"`
	}
	if len(articleIDs) > 0 {
		if err := tx.Table("articles").Select("id").Where("id IN ?", uniqueUintIDs(articleIDs)).Find(&articleRows).Error; err != nil {
			return nil, err
		}
		for _, row := range articleRows {
			articles[row.ID] = struct{}{}
		}
	}
	comments := make(map[uint]struct{})
	var commentRows []struct {
		ID uint `gorm:"column:id"`
	}
	if len(commentIDs) > 0 {
		if err := tx.Table("comments").Select("id").Where("id IN ?", uniqueUintIDs(commentIDs)).Find(&commentRows).Error; err != nil {
			return nil, err
		}
		for _, row := range commentRows {
			comments[row.ID] = struct{}{}
		}
	}
	filtered := make([]models.Notification, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := activeUsers[candidate.RecipientID]; !ok {
			continue
		}
		if _, ok := activeUsers[candidate.ActorID]; !ok {
			continue
		}
		if candidate.ArticleID != nil {
			if _, ok := articles[*candidate.ArticleID]; !ok {
				continue
			}
		}
		if candidate.CommentID != nil {
			if _, ok := comments[*candidate.CommentID]; !ok {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	return filtered, nil
}

func upsertNotificationCandidates(tx *gorm.DB, candidates []models.Notification) error {
	valid := candidates[:0]
	for _, candidate := range candidates {
		if candidate.DedupeKey != "" {
			valid = append(valid, candidate)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for index := range valid {
		valid[index].CreatedAt = now
		valid[index].UpdatedAt = now
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "dedupe_key"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"recipient_id":      gorm.Expr("EXCLUDED.recipient_id"),
			"actor_id":          gorm.Expr("EXCLUDED.actor_id"),
			"notification_type": gorm.Expr("EXCLUDED.notification_type"),
			"article_id":        gorm.Expr("EXCLUDED.article_id"),
			"comment_id":        gorm.Expr("EXCLUDED.comment_id"),
			"source_version":    gorm.Expr("EXCLUDED.source_version"),
			"activity_at":       gorm.Expr("EXCLUDED.activity_at"),
			"read_at":           nil,
			"updated_at":        gorm.Expr("EXCLUDED.updated_at"),
		}),
		Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "notifications.source_version < EXCLUDED.source_version"}}},
	}).Create(&valid).Error
}

func uniqueUintIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

type notificationDLQPayload struct {
	SourceTopic     string    `json:"source_topic"`
	SourcePartition int       `json:"source_partition"`
	SourceOffset    int64     `json:"source_offset"`
	SourceKey       string    `json:"source_key"`
	EventID         string    `json:"event_id,omitempty"`
	Reason          string    `json:"reason"`
	RawValue        string    `json:"raw_value"`
	FailedAt        time.Time `json:"failed_at"`
}

func publishNotificationDLQ(ctx context.Context, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}, message kafka.Message, reason error) error {
	if config.AppConfig == nil || strings.TrimSpace(config.AppConfig.Kafka.NotificationDLQTopic) == "" {
		return errors.New("notification DLQ topic is not configured")
	}
	var envelope struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(message.Value, &envelope)
	payload, err := json.Marshal(notificationDLQPayload{
		SourceTopic: message.Topic, SourcePartition: message.Partition, SourceOffset: message.Offset,
		SourceKey: string(message.Key), EventID: envelope.ID, Reason: reason.Error(), RawValue: string(message.Value), FailedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s:%d:%d", message.Topic, message.Partition, message.Offset)
	return publisher.PublishRaw(ctx, config.AppConfig.Kafka.NotificationDLQTopic, kafka.Message{Key: []byte(key), Value: payload, Time: time.Now().UTC()})
}
