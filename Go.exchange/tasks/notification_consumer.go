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
	"Go.exchange/postmention"

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

type notificationActivityRecord struct {
	Message   kafka.Message
	Envelope  eventing.Envelope
	Candidate *models.Notification
}

type notificationRecordsApplyFunc func(context.Context, []notificationActivityRecord) error

type notificationPostRow struct {
	AuthorID      uint
	Content       string
	ReplyToPostID *uint
	QuotePostID   *uint
}

func notificationPublicPostScope(query *gorm.DB, now time.Time) *gorm.DB {
	return query.Where(`
posts.deleted_at IS NULL
AND posts.visibility = 'public'
AND EXISTS (
    SELECT 1 FROM users AS post_author
    WHERE post_author.id = posts.author_id
      AND post_author.deleted_at IS NULL
)
`)
}

func notificationConsumerConfigured() bool {
	return config.AppConfig != nil &&
		strings.TrimSpace(config.AppConfig.Kafka.ActivityEventsTopic) != "" &&
		strings.TrimSpace(config.AppConfig.Kafka.NotificationGroupID) != "" &&
		strings.TrimSpace(config.AppConfig.Kafka.ConsumerDLQTopic) != ""
}

func startNotificationProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if !notificationConsumerConfigured() {
		return
	}
	for workerID := 1; workerID <= config.NotificationProjectionConsumers(); workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			slotID := id - 1
			KafkaConsumerSlotStarted(PipelineNotificationProjection, slotID)
			defer KafkaConsumerSlotStopped(PipelineNotificationProjection, slotID)
			for {
				runNotificationProjectionConsumer(ctx, slotID)
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

func runNotificationProjectionConsumer(ctx context.Context, slotID int) {
	kafkaConfig := config.AppConfig.Kafka
	reader, clientID, err := eventing.NewKafkaReader(kafkaConfig, kafkaConfig.ActivityEventsTopic, kafkaConfig.NotificationGroupID)
	if err != nil {
		KafkaConsumerSlotFailed(PipelineNotificationProjection, slotID, "kafka_reader_unavailable", pipelineFailureConfiguration)
		log.Printf("[NotificationProjection] create Kafka reader: %v", err)
		return
	}
	trackedReader := trackKafkaConsumerReader(PipelineNotificationProjection, slotID, kafkaConfig.ActivityEventsTopic, kafkaConfig.NotificationGroupID, clientID, reader)
	publisher := rawNotificationPublisher{config: kafkaConfig}
	notificationConsumers.Add(1)
	defer notificationConsumers.Add(-1)
	defer trackedReader.Close()
	if err := consumeNotificationMessages(ctx, trackedReader, publisher); err != nil && ctx.Err() == nil {
		log.Printf("[NotificationProjection] consumer stopped: %v", err)
	}
}

func consumeNotificationMessages(ctx context.Context, reader notificationMessageReader, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}) error {
	for {
		batch := fetchKafkaBatch(ctx, reader, notificationBatchSize, notificationBatchWindow)
		if batch.ContextErr != nil {
			return batch.ContextErr
		}
		_, err := finishKafkaBatch(ctx, PipelineNotificationProjection, reader, batch,
			func(batchCtx context.Context, messages []kafka.Message) error {
				return processNotificationBatch(batchCtx, messages, publisher)
			},
			func(commitCtx context.Context, messages ...kafka.Message) error {
				if err := reader.CommitMessages(commitCtx, messages...); err != nil {
					if commitCtx.Err() != nil {
						return commitCtx.Err()
					}
					commitErr := retryableKafkaError(kafkaFailureCodeKafkaCommit, err)
					metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(commitErr))
					return commitErr
				}
				return nil
			}, nil,
		)
		if err != nil {
			return err
		}
	}
}

type rawNotificationPublisher struct{ config config.KafkaConfig }

func (p rawNotificationPublisher) PublishRaw(ctx context.Context, topic string, messages ...kafka.Message) error {
	return eventing.PublishRawMessages(ctx, p.config, topic, messages...)
}

func processNotificationBatch(ctx context.Context, messages []kafka.Message, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}) error {
	return processNotificationBatchWithRetry(ctx, messages, publisher, defaultKafkaRetryPolicy)
}

func processNotificationBatchWithRetry(ctx context.Context, messages []kafka.Message, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}, policy kafkaRetryPolicy) error {
	return processNotificationBatchWithForeignKeyRecovery(ctx, messages, publisher, policy, applyNotificationRecords, lookupKafkaForeignKeyParent)
}

func processNotificationBatchWithForeignKeyRecovery(ctx context.Context, messages []kafka.Message, publisher interface {
	PublishRaw(context.Context, string, ...kafka.Message) error
}, policy kafkaRetryPolicy, apply notificationRecordsApplyFunc, lookup kafkaForeignKeyParentLookup) error {
	started := time.Now()
	if apply == nil {
		return errors.New("notification database apply function is nil")
	}
	records := make([]notificationActivityRecord, 0, len(messages))
	for _, message := range messages {
		record, err := decodeNotificationActivity(message)
		if err != nil {
			if config.AppConfig == nil {
				return errors.New("notification consumer configuration is not initialized")
			}
			if kafkaFailureClassOf(err) != kafkaFailurePermanent {
				err = permanentKafkaError(kafkaFailureCodeInvalidPayload, err)
			}
			if dlqErr := publishConsumerDLQ(ctx, publisher, config.AppConfig.Kafka.ConsumerDLQTopic, kafkaConsumerNotificationProjection, message, err, 1); dlqErr != nil {
				metrics.RecordNotificationProjectionFailure("dlq")
				metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(dlqErr))
				return fmt.Errorf("publish notification DLQ: %w", dlqErr)
			}
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCode(err))
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}
	var terminalDLQ map[kafkaMessageIdentity]struct{}
	if err := retryNotificationDatabaseApply(ctx, policy, func() error {
		pending := records
		if len(terminalDLQ) > 0 {
			pending = make([]notificationActivityRecord, 0, len(records))
			for _, record := range records {
				if _, terminal := terminalDLQ[kafkaMessageIdentityOf(record.Message)]; !terminal {
					pending = append(pending, record)
				}
			}
		}
		applySubset := func(applyCtx context.Context, subset []notificationActivityRecord) error {
			applyErr := apply(applyCtx, subset)
			if applyErr == nil || kafkaFailureClassOf(applyErr) != "" {
				return applyErr
			}
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, applyErr)
		}
		return applyKafkaBatchWithForeignKeyIsolation(ctx, pending, applySubset,
			func(err error) bool {
				return kafkaPostgresForeignKeyIsAllowed(err,
					"fk_notifications_recipient", "fk_notifications_actor", "fk_notifications_post")
			},
			func(handleCtx context.Context, record notificationActivityRecord, _ error) error {
				// Re-run the singleton through the existing candidate filter after the
				// failed transaction has rolled back. If a concurrent deletion is now
				// visible to the filter, Inbox can complete as a normal no-op.
				secondErr := applySubset(handleCtx, []notificationActivityRecord{record})
				if secondErr == nil {
					return nil
				}
				if !kafkaPostgresForeignKeyIsAllowed(secondErr,
					"fk_notifications_recipient", "fk_notifications_actor", "fk_notifications_post") {
					return secondErr
				}
				constraint, _ := kafkaPostgresForeignKeyConstraint(secondErr)
				userID, postID, ok := notificationForeignKeyParentIDs(record, constraint)
				if !ok {
					return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, secondErr)
				}
				if config.AppConfig == nil {
					return retryableKafkaError(kafkaFailureCodeInternalState, errors.New("notification consumer configuration is not initialized"))
				}
				dlqErr := publishConfirmedMissingParentKafkaForeignKeyDLQ(
					handleCtx, global.WorkerDb, lookup, publisher,
					config.AppConfig.Kafka.ConsumerDLQTopic, kafkaConsumerNotificationProjection,
					record.Message, secondErr, userID, postID,
				)
				if dlqErr != nil {
					metrics.RecordNotificationProjectionFailure("dlq")
					metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(dlqErr))
					return dlqErr
				}
				log.Printf("[NotificationProjection] route permanent foreign-key message to DLQ topic=%s partition=%d offset=%d constraint=%s", record.Message.Topic, record.Message.Partition, record.Message.Offset, constraint)
				metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCodeDatabaseForeignKey)
				if terminalDLQ == nil {
					terminalDLQ = make(map[kafkaMessageIdentity]struct{})
				}
				terminalDLQ[kafkaMessageIdentityOf(record.Message)] = struct{}{}
				return nil
			},
		)
	}); err != nil {
		metrics.RecordNotificationProjectionFailure("database")
		return fmt.Errorf("apply notification records: %w", err)
	}
	metrics.ObserveNotificationProjectionLatency(time.Since(started))
	return nil
}

func notificationForeignKeyParentIDs(record notificationActivityRecord, constraint string) (uint, uint, bool) {
	if record.Candidate == nil {
		return 0, 0, false
	}
	switch constraint {
	case "fk_notifications_recipient":
		return record.Candidate.RecipientID, 0, record.Candidate.RecipientID != 0
	case "fk_notifications_actor":
		return record.Candidate.ActorID, 0, record.Candidate.ActorID != 0
	case "fk_notifications_post":
		if record.Candidate.PostID == nil || *record.Candidate.PostID == 0 {
			return 0, 0, false
		}
		return 0, *record.Candidate.PostID, true
	default:
		return 0, 0, false
	}
}

func retryNotificationDatabaseApply(ctx context.Context, policy kafkaRetryPolicy, apply func() error) error {
	if apply == nil {
		return errors.New("notification database apply operation is nil")
	}
	attempts := 0
	err := retryKafkaOperation(ctx, policy, func(attempt int, retryErr error) {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCode(retryErr))
		log.Printf("[NotificationProjection] retry attempt=%d max_attempts=%d code=%s", attempt, policy.MaxAttempts, kafkaFailureCode(retryErr))
	}, func() error {
		attempts++
		if applyErr := apply(); applyErr != nil {
			if kafkaFailureClassOf(applyErr) != "" {
				return applyErr
			}
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, applyErr)
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if kafkaFailureClassOf(err) == kafkaFailureRetryable && attempts >= policy.MaxAttempts {
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCode(err))
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerNotificationProjection, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(err))
	}
	return err
}

func decodeNotificationActivity(message kafka.Message) (notificationActivityRecord, error) {
	envelope, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, err)
	}
	if _, err := uuid.Parse(strings.TrimSpace(envelope.ID)); err != nil {
		return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("notification activity id must be a UUID"))
	}
	if envelope.SchemaVersion < 1 || strings.TrimSpace(envelope.AggregateType) == "" || strings.TrimSpace(envelope.AggregateID) == "" || envelope.OccurredAt.IsZero() {
		return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("notification activity envelope is missing required fields"))
	}
	var payloadObject map[string]json.RawMessage
	if len(envelope.Payload) == 0 || json.Unmarshal(envelope.Payload, &payloadObject) != nil || payloadObject == nil {
		return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("notification activity payload must be a JSON object"))
	}
	record := notificationActivityRecord{Message: message, Envelope: envelope}
	switch envelope.Type {
	case eventing.EventTypePostReactionApplied:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "post_reaction" {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, errors.New("unsupported post reaction activity schema"))
		}
		var payload eventing.PostReactionAppliedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode post reaction activity payload: %w", err))
		}
		if payload.ActorID == 0 || payload.PostID == 0 || payload.PostAuthorID == 0 || payload.ReactionVersion <= 0 || payload.StateChangedAt.IsZero() || !payload.StateChangedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != fmt.Sprintf("%d:%d", payload.ActorID, payload.PostID) {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("invalid post reaction activity payload"))
		}
		if !payload.Liked || payload.ActorID == payload.PostAuthorID {
			return record, nil
		}
		postID := payload.PostID
		record.Candidate = &models.Notification{
			RecipientID: payload.PostAuthorID, ActorID: payload.ActorID, Type: models.NotificationTypePostLiked,
			PostID: &postID, DedupeKey: fmt.Sprintf("post_like:%d:%d", payload.ActorID, payload.PostID),
			SourceVersion: payload.ReactionVersion, ActivityAt: payload.StateChangedAt,
		}
	case eventing.EventTypeReplyCreated:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "post" {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, errors.New("unsupported reply activity schema"))
		}
		var payload eventing.ReplyCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode reply activity payload: %w", err))
		}
		if payload.ReplyPostID == 0 || payload.ParentPostID == 0 || payload.ConversationID == 0 || payload.ActorID == 0 || payload.ParentAuthorID == 0 || payload.CreatedAt.IsZero() || !payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != strconv.FormatUint(uint64(payload.ReplyPostID), 10) {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("invalid reply activity payload"))
		}
		if payload.ActorID == payload.ParentAuthorID {
			return record, nil
		}
		record.Candidate = &models.Notification{
			RecipientID: payload.ParentAuthorID, ActorID: payload.ActorID, Type: models.NotificationTypePostReplied,
			PostID:    func() *uint { value := payload.ReplyPostID; return &value }(),
			DedupeKey: fmt.Sprintf("post_reply:%d", payload.ReplyPostID), ActivityAt: payload.CreatedAt,
		}
	case eventing.EventTypeQuoteCreated:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "post" {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, errors.New("unsupported quote activity schema"))
		}
		var payload eventing.QuoteCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode quote activity payload: %w", err))
		}
		if payload.QuotePostID == 0 || payload.TargetPostID == 0 || payload.ActorID == 0 || payload.TargetAuthorID == 0 || payload.CreatedAt.IsZero() ||
			!payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != strconv.FormatUint(uint64(payload.QuotePostID), 10) {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("invalid quote activity payload"))
		}
		if payload.ActorID == payload.TargetAuthorID {
			return record, nil
		}
		quotePostID := payload.QuotePostID
		record.Candidate = &models.Notification{
			RecipientID: payload.TargetAuthorID, ActorID: payload.ActorID, Type: models.NotificationTypePostQuoted,
			PostID: &quotePostID, DedupeKey: fmt.Sprintf("post_quote:%d", payload.QuotePostID),
			SourceVersion: 0, ActivityAt: payload.CreatedAt,
		}
	case eventing.EventTypeUserFollowCreated:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "user_follow" {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, errors.New("unsupported follow activity schema"))
		}
		var payload eventing.UserFollowCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode follow activity payload: %w", err))
		}
		if payload.FollowID == 0 || payload.FollowerID == 0 || payload.FollowingID == 0 || payload.FollowerID == payload.FollowingID || payload.CreatedAt.IsZero() || !payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != strconv.FormatUint(uint64(payload.FollowID), 10) {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("invalid follow activity payload"))
		}
		if payload.FollowerID == payload.FollowingID {
			return record, nil
		}
		record.Candidate = &models.Notification{
			RecipientID: payload.FollowingID, ActorID: payload.FollowerID, Type: models.NotificationTypeUserFollowed,
			DedupeKey:     fmt.Sprintf("user_followed:%d:%d", payload.FollowerID, payload.FollowingID),
			SourceVersion: int64(payload.FollowID), ActivityAt: payload.CreatedAt,
		}
	case eventing.EventTypePostMentioned:
		if envelope.SchemaVersion != 1 || envelope.AggregateType != "post_mention" {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, errors.New("unsupported post mention activity schema"))
		}
		for _, field := range []string{"post_id", "actor_id", "recipient_id", "created_at"} {
			if _, exists := payloadObject[field]; !exists {
				return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post mention activity payload is incomplete"))
			}
		}
		if len(payloadObject) != 4 {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("post mention activity payload has unknown fields"))
		}
		var payload eventing.PostMentionedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, fmt.Errorf("decode post mention activity payload: %w", err))
		}
		if payload.PostID == 0 || payload.ActorID == 0 || payload.RecipientID == 0 || payload.ActorID == payload.RecipientID || payload.CreatedAt.IsZero() ||
			!payload.CreatedAt.Equal(envelope.OccurredAt) || envelope.AggregateID != fmt.Sprintf("%d:%d", payload.PostID, payload.RecipientID) {
			return notificationActivityRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("invalid post mention activity payload"))
		}
		postID := payload.PostID
		record.Candidate = &models.Notification{
			RecipientID: payload.RecipientID, ActorID: payload.ActorID, Type: models.NotificationTypePostMentioned,
			PostID: &postID, DedupeKey: fmt.Sprintf("post_mentioned:%d:%d", payload.PostID, payload.RecipientID),
			SourceVersion: 0, ActivityAt: payload.CreatedAt,
		}
	default:
		// A well-formed unknown event is a forward-compatible no-op.
	}
	return record, nil
}

func applyNotificationRecords(ctx context.Context, records []notificationActivityRecord) error {
	if ctx == nil {
		return errors.New("notification apply context is nil")
	}
	if global.WorkerDb == nil {
		return errors.New("database is not initialized")
	}
	if config.AppConfig == nil || strings.TrimSpace(config.AppConfig.Kafka.NotificationGroupID) == "" {
		return errors.New("notification consumer group is not configured")
	}
	return global.WorkerDb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
	participantIDs := make([]uint, 0, len(candidates)*2)
	postIDs := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		participantIDs = append(participantIDs, candidate.RecipientID, candidate.ActorID)
		if candidate.PostID != nil {
			postIDs = append(postIDs, *candidate.PostID)
		}
	}
	activeUsers := make(map[uint]struct{})
	var participants []struct {
		ID uint `gorm:"column:id"`
	}
	if err := tx.Model(&models.User{}).Select("id").Where("id IN ? AND deleted_at IS NULL", uniqueUintIDs(participantIDs)).Find(&participants).Error; err != nil {
		return nil, err
	}
	for _, user := range participants {
		activeUsers[user.ID] = struct{}{}
	}
	posts := make(map[uint]notificationPostRow)
	var postRows []struct {
		ID            uint   `gorm:"column:id"`
		AuthorID      uint   `gorm:"column:author_id"`
		Content       string `gorm:"column:content"`
		ReplyToPostID *uint  `gorm:"column:reply_to_post_id"`
		QuotePostID   *uint  `gorm:"column:quote_post_id"`
	}
	if len(postIDs) > 0 {
		now := time.Now().UTC()
		if err := notificationPublicPostScope(tx.Table("posts"), now).
			Select("posts.id, posts.author_id, posts.content, posts.reply_to_post_id, posts.quote_post_id").
			Where("posts.id IN ?", uniqueUintIDs(postIDs)).Find(&postRows).Error; err != nil {
			return nil, err
		}
		for _, row := range postRows {
			posts[row.ID] = notificationPostRow{AuthorID: row.AuthorID, Content: row.Content, ReplyToPostID: row.ReplyToPostID, QuotePostID: row.QuotePostID}
		}
	}
	mentionTokensByPost := make(map[uint][]string)
	allMentionTokens := make([]string, 0)
	seenMentionTokens := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Type != models.NotificationTypePostMentioned || candidate.PostID == nil {
			continue
		}
		post, ok := posts[*candidate.PostID]
		if !ok {
			continue
		}
		tokens, err := postmention.ExtractTokens(post.Content)
		if err != nil {
			continue
		}
		mentionTokensByPost[*candidate.PostID] = tokens
		for _, token := range tokens {
			if _, exists := seenMentionTokens[token]; exists {
				continue
			}
			seenMentionTokens[token] = struct{}{}
			allMentionTokens = append(allMentionTokens, token)
		}
	}
	mentionedUsers := make(map[string]uint)
	if len(allMentionTokens) > 0 {
		var users []struct {
			ID       uint   `gorm:"column:id"`
			Username string `gorm:"column:username"`
		}
		if err := tx.Model(&models.User{}).Select("id, username").Where("username IN ? AND deleted_at IS NULL", allMentionTokens).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, user := range users {
			mentionedUsers[user.Username] = user.ID
		}
	}
	referencedPostIDs := make([]uint, 0, len(postRows)*2)
	for _, row := range postRows {
		if row.ReplyToPostID != nil && *row.ReplyToPostID != 0 {
			referencedPostIDs = append(referencedPostIDs, *row.ReplyToPostID)
		}
		if row.QuotePostID != nil && *row.QuotePostID != 0 {
			referencedPostIDs = append(referencedPostIDs, *row.QuotePostID)
		}
	}
	referencedAuthors := make(map[uint]uint)
	if len(referencedPostIDs) > 0 {
		var referencedRows []struct {
			ID       uint `gorm:"column:id"`
			AuthorID uint `gorm:"column:author_id"`
		}
		if err := tx.Unscoped().Table("posts").Select("id, author_id").Where("id IN ?", uniqueUintIDs(referencedPostIDs)).Find(&referencedRows).Error; err != nil {
			return nil, err
		}
		for _, row := range referencedRows {
			referencedAuthors[row.ID] = row.AuthorID
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
		if candidate.PostID != nil {
			post, ok := posts[*candidate.PostID]
			if !ok {
				continue
			}
			switch candidate.Type {
			case models.NotificationTypePostLiked:
				if candidate.SourceVersion <= 0 || post.AuthorID != candidate.RecipientID {
					continue
				}
			case models.NotificationTypePostReplied:
				if candidate.SourceVersion != 0 || post.ReplyToPostID == nil || referencedAuthors[*post.ReplyToPostID] != candidate.RecipientID {
					continue
				}
			case models.NotificationTypePostQuoted:
				if candidate.SourceVersion != 0 || post.QuotePostID == nil || referencedAuthors[*post.QuotePostID] != candidate.RecipientID {
					continue
				}
			case models.NotificationTypePostMentioned:
				if candidate.SourceVersion != 0 || candidate.ActorID == candidate.RecipientID || post.AuthorID != candidate.ActorID || !mentionsUser(mentionTokensByPost[*candidate.PostID], candidate.RecipientID, mentionedUsers) {
					continue
				}
				if (post.ReplyToPostID != nil && referencedAuthors[*post.ReplyToPostID] == candidate.RecipientID) ||
					(post.QuotePostID != nil && referencedAuthors[*post.QuotePostID] == candidate.RecipientID) {
					continue
				}
			default:
				continue
			}
		} else if candidate.Type != models.NotificationTypeUserFollowed || candidate.SourceVersion <= 0 {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered, nil
}

func mentionsUser(tokens []string, userID uint, users map[string]uint) bool {
	for _, token := range tokens {
		if users[token] == userID {
			return true
		}
	}
	return false
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
			"post_id":           gorm.Expr("EXCLUDED.post_id"),
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
