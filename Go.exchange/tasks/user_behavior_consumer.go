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
	userBehaviorConsumerRetryDelay = 2 * time.Second
	userBehaviorBatchSize          = 500
	userBehaviorBatchWindow        = 50 * time.Millisecond
)

type userBehaviorMessageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type userBehaviorEventRecord struct {
	Envelope eventing.Envelope
	Payload  eventing.UserBehaviorPayload
}

type userBehaviorRecordsApplyFunc func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error

type userBehaviorPair struct {
	UserID uint
	PostID uint
}

type userBehaviorViewAggregate struct {
	Key        userBehaviorPair
	Count      int64
	LastSeenAt time.Time
}

type userBehaviorReactionCandidate struct {
	Key      userBehaviorPair
	Envelope eventing.Envelope
	Payload  eventing.UserBehaviorPayload
	Liked    bool
}

func startUserBehaviorProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil ||
		strings.TrimSpace(config.AppConfig.Kafka.UserBehaviorTopic) == "" ||
		strings.TrimSpace(config.AppConfig.Kafka.UserBehaviorGroupID) == "" {
		return
	}
	for workerID := 1; workerID <= config.LikeBehaviorProjectionConsumers(); workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			PipelineStarted(PipelineUserBehaviorProjection)
			defer PipelineStopped(PipelineUserBehaviorProjection)
			for {
				runUserBehaviorProjectionConsumer(ctx)
				if ctx.Err() != nil {
					return
				}
				log.Printf("[BehaviorProjection:%d] consumer stopped; retrying in %s", id, userBehaviorConsumerRetryDelay)
				select {
				case <-ctx.Done():
					return
				case <-time.After(userBehaviorConsumerRetryDelay):
				}
			}
		}(workerID)
	}
}

func runUserBehaviorProjectionConsumer(ctx context.Context) {
	kafkaConfig := config.AppConfig.Kafka
	reader, err := eventing.NewKafkaReader(
		kafkaConfig,
		kafkaConfig.UserBehaviorTopic,
		kafkaConfig.UserBehaviorGroupID,
	)
	if err != nil {
		PipelineFailure(PipelineUserBehaviorProjection, "kafka_reader_unavailable", 0)
		log.Printf("[BehaviorProjection] create Kafka reader: %v", err)
		return
	}
	userBehaviorConsumers.Add(1)
	defer userBehaviorConsumers.Add(-1)
	defer reader.Close()
	publisher := eventingRawKafkaMessagePublisher{kafkaConfig: kafkaConfig}
	if err := consumeUserBehaviorMessages(ctx, reader, publisher, global.WorkerDb, kafkaConfig); err != nil && ctx.Err() == nil {
		PipelineFailure(PipelineUserBehaviorProjection, "projection_failed", 0)
		log.Printf("[BehaviorProjection] consumer stopped: %v", err)
	}
}

func consumeUserBehaviorMessages(
	ctx context.Context,
	reader userBehaviorMessageReader,
	publisher rawKafkaMessagePublisher,
	db *gorm.DB,
	kafkaConfig config.KafkaConfig,
) error {
	return consumeUserBehaviorMessagesWithApply(ctx, reader, publisher, db, kafkaConfig, defaultKafkaRetryPolicy, applyUserBehaviorRecords)
}

func consumeUserBehaviorMessagesWithApply(
	ctx context.Context,
	reader userBehaviorMessageReader,
	publisher rawKafkaMessagePublisher,
	db *gorm.DB,
	kafkaConfig config.KafkaConfig,
	policy kafkaRetryPolicy,
	apply userBehaviorRecordsApplyFunc,
) error {
	if ctx == nil {
		return errors.New("user behavior consumer context is nil")
	}
	if reader == nil {
		return errors.New("user behavior message reader is nil")
	}
	if apply == nil {
		return errors.New("user behavior apply function is nil")
	}
	for {
		first, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] fetch Kafka message: %v", err)
			}
			return err
		}
		batch := collectUserBehaviorBatch(ctx, reader, first)
		records, err := classifyUserBehaviorBatch(ctx, publisher, kafkaConfig, batch)
		if err != nil {
			return fmt.Errorf("classify user behavior batch of %d messages: %w", len(batch), err)
		}
		if len(records) > 0 {
			attempts := 0
			err = retryKafkaOperation(ctx, policy, func(attempt int, retryErr error) {
				metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCode(retryErr))
				log.Printf("[BehaviorProjection] retry attempt=%d max_attempts=%d code=%s", attempt, policy.MaxAttempts, kafkaFailureCode(retryErr))
			}, func() error {
				attempts++
				applyErr := apply(ctx, db, strings.TrimSpace(kafkaConfig.UserBehaviorGroupID), kafkaConfig, records)
				return applyErr
			})
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if kafkaFailureClassOf(err) == kafkaFailureRetryable && attempts >= policy.MaxAttempts {
					metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCode(err))
				}
				return fmt.Errorf("apply user behavior batch of %d messages: %w", len(batch), err)
			}
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeBatchApplied, kafkaRecoveryCodeNone)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reader.CommitMessages(ctx, batch...); err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] commit Kafka batch: %v", err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			commitErr := retryableKafkaError(kafkaFailureCodeKafkaCommit, err)
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeRedeliveryRequired, kafkaFailureCode(commitErr))
			return commitErr
		}
		backlog := int64(0)
		if statsReader, ok := reader.(interface{ Stats() kafka.ReaderStats }); ok {
			backlog = kafkaBacklog(statsReader)
		}
		PipelineCommit(PipelineUserBehaviorProjection, time.Now().UTC(), backlog)
	}
}

func collectUserBehaviorBatch(ctx context.Context, reader userBehaviorMessageReader, first kafka.Message) []kafka.Message {
	batch := []kafka.Message{first}
	if len(batch) >= userBehaviorBatchSize {
		return batch
	}
	collectCtx, cancel := context.WithTimeout(ctx, userBehaviorBatchWindow)
	defer cancel()
	for len(batch) < userBehaviorBatchSize {
		message, err := reader.FetchMessage(collectCtx)
		if err != nil {
			if collectCtx.Err() != nil || ctx.Err() != nil {
				break
			}
			log.Printf("[BehaviorProjection] collect Kafka message: %v", err)
			break
		}
		batch = append(batch, message)
	}
	return batch
}

func decodeUserBehaviorMessage(message kafka.Message) (userBehaviorEventRecord, error) {
	const currentSchemaVersion = 1
	event, err := eventing.DecodeEnvelope(message.Value)
	if err != nil {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeDecodeEnvelope, err)
	}
	if !isUserBehaviorEvent(event.Type) {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedEvent, fmt.Errorf("unsupported user behavior event type %q", event.Type))
	}
	if event.SchemaVersion != currentSchemaVersion {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeUnsupportedSchema, fmt.Errorf("unsupported user behavior schema version %d", event.SchemaVersion))
	}
	var payload eventing.UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeDecodePayload, err)
	}
	if payload.UserID == 0 || payload.PostID == 0 {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("user behavior payload requires user_id and post_id"))
	}
	if (event.Type == eventing.EventTypePostLiked || event.Type == eventing.EventTypePostUnliked) && payload.LikeVersion <= 0 {
		return userBehaviorEventRecord{}, permanentKafkaError(kafkaFailureCodeInvalidPayload, errors.New("like behavior payload requires positive like_version"))
	}
	return userBehaviorEventRecord{Envelope: event, Payload: payload}, nil
}

func classifyUserBehaviorBatch(
	ctx context.Context,
	publisher rawKafkaMessagePublisher,
	kafkaConfig config.KafkaConfig,
	messages []kafka.Message,
) ([]userBehaviorEventRecord, error) {
	if ctx == nil {
		return nil, errors.New("user behavior batch context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	valid := make([]userBehaviorEventRecord, 0, len(messages))
	type permanentMessage struct {
		message kafka.Message
		err     error
	}
	permanent := make([]permanentMessage, 0)
	for _, message := range messages {
		record, err := decodeUserBehaviorMessage(message)
		if err != nil {
			if kafkaFailureClassOf(err) != kafkaFailurePermanent {
				return nil, err
			}
			permanent = append(permanent, permanentMessage{message: message, err: err})
			continue
		}
		valid = append(valid, record)
	}
	for _, failure := range permanent {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		log.Printf("[BehaviorProjection] route permanent message to DLQ topic=%s partition=%d offset=%d code=%s", failure.message.Topic, failure.message.Partition, failure.message.Offset, kafkaFailureCode(failure.err))
		if err := publishConsumerDLQ(ctx, publisher, kafkaConfig.ConsumerDLQTopic, kafkaConsumerUserBehaviorProjection, failure.message, failure.err, 1); err != nil {
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeDLQPublishFailed, kafkaFailureCode(err))
			return nil, err
		}
		metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeMessageDLQ, kafkaFailureCode(failure.err))
	}
	seenEventIDs := make(map[string]struct{}, len(valid))
	records := make([]userBehaviorEventRecord, 0, len(valid))
	for _, record := range valid {
		if _, exists := seenEventIDs[record.Envelope.ID]; exists {
			log.Printf("[BehaviorProjection] skip duplicate event in fetched batch: %s", record.Envelope.ID)
			metrics.RecordKafkaConsumerRecovery(kafkaConsumerUserBehaviorProjection, kafkaRecoveryOutcomeMessageNoop, kafkaRecoveryCodeNone)
			continue
		}
		seenEventIDs[record.Envelope.ID] = struct{}{}
		records = append(records, record)
	}
	return records, nil
}

func isUserBehaviorEvent(eventType string) bool {
	return eventType == eventing.EventTypePostViewed ||
		eventType == eventing.EventTypePostLiked ||
		eventType == eventing.EventTypePostUnliked
}

// ConsumerInbox retention must be coordinated with Kafka retention, the replay
// window, and a future rebuild strategy before automatic cleanup is introduced.
func applyUserBehaviorRecords(ctx context.Context, db *gorm.DB, consumerName string, kafkaConfig config.KafkaConfig, records []userBehaviorEventRecord) error {
	if ctx == nil {
		return errors.New("user behavior apply context is nil")
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
		return errors.New("user behavior consumer group is not configured")
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

		viewAggregates := aggregateUserBehaviorViews(records, firstDelivery)
		if err := bulkUpsertPostViewBehavior(tx, viewAggregates); err != nil {
			return err
		}

		viewCountDeltas := aggregatePostViewCountDeltas(records, firstDelivery)
		if err := bulkIncrementPostViewCounts(tx, viewCountDeltas); err != nil {
			return err
		}

		reactions := collapseUserBehaviorReactions(records, firstDelivery)
		applied, err := bulkUpsertPostReactionsReturningApplied(tx, reactions)
		if err != nil {
			return err
		}
		if err := appendAppliedReactionActivities(tx, kafkaConfig, applied); err != nil {
			return err
		}
		return recommendation.InvalidateProfiles(tx, userBehaviorProfileInvalidationUsers(records, firstDelivery), "user_behavior_projection", time.Now().UTC())
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err)
	}
	return nil
}

func userBehaviorProfileInvalidationUsers(records []userBehaviorEventRecord, firstDelivery map[string]struct{}) []uint {
	seen := make(map[uint]struct{})
	for _, record := range records {
		if _, ok := firstDelivery[record.Envelope.ID]; !ok || record.Payload.UserID == 0 || record.Payload.PostID == 0 {
			continue
		}
		switch record.Envelope.Type {
		case eventing.EventTypePostViewed, eventing.EventTypePostLiked, eventing.EventTypePostUnliked:
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

func aggregatePostViewCountDeltas(
	records []userBehaviorEventRecord,
	firstDelivery map[string]struct{},
) map[uint]int64 {
	deltas := make(map[uint]int64)
	for _, record := range records {
		if record.Envelope.Type != eventing.EventTypePostViewed {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok || record.Payload.PostID == 0 {
			continue
		}
		deltas[record.Payload.PostID]++
	}
	return deltas
}

func bulkIncrementPostViewCounts(tx *gorm.DB, deltas map[uint]int64) error {
	if len(deltas) == 0 {
		return nil
	}

	postIDs := make([]uint, 0, len(deltas))
	for postID := range deltas {
		postIDs = append(postIDs, postID)
	}
	sort.Slice(postIDs, func(i, j int) bool {
		return postIDs[i] < postIDs[j]
	})

	var cases strings.Builder
	cases.WriteString("CASE id")
	args := make([]interface{}, 0, len(postIDs)*2)
	for _, postID := range postIDs {
		cases.WriteString(" WHEN ? THEN ?")
		args = append(args, postID, deltas[postID])
	}
	cases.WriteString(" ELSE 0 END")

	return tx.Model(&models.Post{}).
		Where("id IN ?", postIDs).
		UpdateColumn("view_count", gorm.Expr("view_count + ("+cases.String()+")", args...)).
		Error
}

func aggregateUserBehaviorViews(
	records []userBehaviorEventRecord,
	firstDelivery map[string]struct{},
) []userBehaviorViewAggregate {
	byPair := make(map[userBehaviorPair]userBehaviorViewAggregate)
	for _, record := range records {
		if record.Envelope.Type != eventing.EventTypePostViewed {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		key := userBehaviorPair{UserID: record.Payload.UserID, PostID: record.Payload.PostID}
		current := byPair[key]
		current.Key = key
		current.Count++
		occurredAt := userBehaviorOccurredAt(record.Envelope)
		if current.LastSeenAt.IsZero() || occurredAt.After(current.LastSeenAt) {
			current.LastSeenAt = occurredAt
		}
		byPair[key] = current
	}

	keys := make([]userBehaviorPair, 0, len(byPair))
	for key := range byPair {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].UserID != keys[j].UserID {
			return keys[i].UserID < keys[j].UserID
		}
		return keys[i].PostID < keys[j].PostID
	})
	result := make([]userBehaviorViewAggregate, 0, len(keys))
	for _, key := range keys {
		result = append(result, byPair[key])
	}
	return result
}

func userBehaviorOccurredAt(event eventing.Envelope) time.Time {
	if event.OccurredAt.IsZero() {
		return time.Now().UTC()
	}
	return event.OccurredAt.UTC()
}

func bulkUpsertPostViewBehavior(tx *gorm.DB, aggregates []userBehaviorViewAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	rows := make([]models.PostBehavior, 0, len(aggregates))
	for _, aggregate := range aggregates {
		rows = append(rows, models.PostBehavior{
			Model:      gorm.Model{CreatedAt: updatedAt, UpdatedAt: updatedAt},
			UserID:     aggregate.Key.UserID,
			PostID:     aggregate.Key.PostID,
			Action:     "view",
			Count:      aggregate.Count,
			LastSeenAt: aggregate.LastSeenAt,
			Active:     true,
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

func collapseUserBehaviorReactions(
	records []userBehaviorEventRecord,
	firstDelivery map[string]struct{},
) []userBehaviorReactionCandidate {
	byPair := make(map[userBehaviorPair]userBehaviorReactionCandidate)
	for _, record := range records {
		if record.Envelope.Type != eventing.EventTypePostLiked &&
			record.Envelope.Type != eventing.EventTypePostUnliked {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		key := userBehaviorPair{UserID: record.Payload.UserID, PostID: record.Payload.PostID}
		candidate := userBehaviorReactionCandidate{
			Key:      key,
			Envelope: record.Envelope,
			Payload:  record.Payload,
			Liked:    record.Envelope.Type == eventing.EventTypePostLiked,
		}
		current, exists := byPair[key]
		if !exists || candidate.Payload.LikeVersion > current.Payload.LikeVersion {
			byPair[key] = candidate
			continue
		}
		if candidate.Payload.LikeVersion == current.Payload.LikeVersion && candidate.Liked != current.Liked {
			log.Printf(
				"[BehaviorProjection] conflicting equal like_version user=%d post=%d version=%d; keeping earliest Kafka event",
				key.UserID, key.PostID, candidate.Payload.LikeVersion,
			)
		}
	}

	keys := make([]userBehaviorPair, 0, len(byPair))
	for key := range byPair {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].UserID != keys[j].UserID {
			return keys[i].UserID < keys[j].UserID
		}
		return keys[i].PostID < keys[j].PostID
	})
	result := make([]userBehaviorReactionCandidate, 0, len(keys))
	for _, key := range keys {
		result = append(result, byPair[key])
	}
	return result
}

func applyPostReactionProjection(tx *gorm.DB, eventType string, payload eventing.UserBehaviorPayload, occurredAt time.Time) error {
	if eventType != eventing.EventTypePostLiked && eventType != eventing.EventTypePostUnliked {
		return nil
	}
	if payload.LikeVersion <= 0 {
		return nil
	}
	candidate := userBehaviorReactionCandidate{
		Key:      userBehaviorPair{UserID: payload.UserID, PostID: payload.PostID},
		Envelope: eventing.Envelope{Type: eventType, OccurredAt: occurredAt},
		Payload:  payload,
		Liked:    eventType == eventing.EventTypePostLiked,
	}
	return bulkUpsertPostReactions(tx, []userBehaviorReactionCandidate{candidate})
}

type appliedPostReaction struct {
	UserID         uint      `gorm:"column:user_id"`
	PostID         uint      `gorm:"column:post_id"`
	Version        int64     `gorm:"column:reaction_version"`
	Liked          bool      `gorm:"column:liked"`
	StateChangedAt time.Time `gorm:"column:state_changed_at"`
}

// bulkUpsertPostReactionsReturningApplied is the authoritative version
// gate. PostgreSQL returns only inserts and updates whose incoming version was
// newer than the stored version, which prevents stale deliveries from
// generating activity or notifications.
func bulkUpsertPostReactionsReturningApplied(tx *gorm.DB, candidates []userBehaviorReactionCandidate) ([]appliedPostReaction, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	values := make([]string, 0, len(candidates))
	args := make([]interface{}, 0, len(candidates)*7)
	updatedAt := time.Now().UTC()
	for index, candidate := range candidates {
		base := index*7 + 1
		values = append(values, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d)", base, base+1, base+2, base+3, base+4, base+5, base+6))
		args = append(args,
			candidate.Key.UserID, candidate.Key.PostID, models.PostReactionLike,
			candidate.Liked, candidate.Payload.LikeVersion, updatedAt, userBehaviorOccurredAt(candidate.Envelope),
		)
	}
	query := `
INSERT INTO post_reaction
  (user_id, post_id, reaction, liked, reaction_version, updated_at, state_changed_at)
VALUES ` + strings.Join(values, ",") + `
ON CONFLICT (user_id, post_id) DO UPDATE SET
  reaction = EXCLUDED.reaction,
  liked = EXCLUDED.liked,
  reaction_version = EXCLUDED.reaction_version,
  state_changed_at = EXCLUDED.state_changed_at,
  updated_at = EXCLUDED.updated_at
WHERE post_reaction.reaction_version < EXCLUDED.reaction_version
RETURNING user_id, post_id, reaction_version, liked, state_changed_at`
	var applied []appliedPostReaction
	if err := tx.Raw(query, args...).Scan(&applied).Error; err != nil {
		return nil, err
	}
	return applied, nil
}

func appendAppliedReactionActivities(tx *gorm.DB, kafkaConfig config.KafkaConfig, applied []appliedPostReaction) error {
	if len(applied) == 0 {
		return nil
	}
	if strings.TrimSpace(kafkaConfig.ActivityEventsTopic) == "" {
		return errors.New("Kafka activity events topic is not configured")
	}
	postIDs := make([]uint, 0, len(applied))
	seen := make(map[uint]struct{}, len(applied))
	for _, reaction := range applied {
		if _, exists := seen[reaction.PostID]; !exists {
			seen[reaction.PostID] = struct{}{}
			postIDs = append(postIDs, reaction.PostID)
		}
	}
	type postAuthorRow struct {
		ID       uint `gorm:"column:id"`
		AuthorID uint `gorm:"column:author_id"`
	}
	var rows []postAuthorRow
	if err := tx.Table("posts").Select("id, author_id").Where("id IN ?", postIDs).Find(&rows).Error; err != nil {
		return err
	}
	authors := make(map[uint]uint, len(rows))
	for _, row := range rows {
		authors[row.ID] = row.AuthorID
	}
	for _, reaction := range applied {
		authorID := authors[reaction.PostID]
		if authorID == 0 {
			return fmt.Errorf("post %d author is missing for reaction activity", reaction.PostID)
		}
		envelope, err := eventing.NewPostReactionAppliedEnvelope(uuid.NewString(), eventing.PostReactionAppliedPayload{
			ActorID: reaction.UserID, PostID: reaction.PostID, PostAuthorID: authorID,
			Liked: reaction.Liked, ReactionVersion: reaction.Version, StateChangedAt: reaction.StateChangedAt,
		})
		if err != nil {
			return err
		}
		outboxEvent, err := eventing.NewOutboxEvent(kafkaConfig, envelope)
		if err != nil {
			return err
		}
		if err := eventing.AddOutboxEvent(tx, outboxEvent); err != nil {
			return err
		}
	}
	return nil
}

func bulkUpsertPostReactions(tx *gorm.DB, candidates []userBehaviorReactionCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	rows := make([]models.PostReaction, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, models.PostReaction{
			UserID:         candidate.Key.UserID,
			PostID:         candidate.Key.PostID,
			Reaction:       models.PostReactionLike,
			Liked:          candidate.Liked,
			Version:        candidate.Payload.LikeVersion,
			UpdatedAt:      updatedAt,
			StateChangedAt: userBehaviorOccurredAt(candidate.Envelope),
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "post_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"reaction":         gorm.Expr("EXCLUDED.reaction"),
			"liked":            gorm.Expr("EXCLUDED.liked"),
			"reaction_version": gorm.Expr("EXCLUDED.reaction_version"),
			"state_changed_at": gorm.Expr("EXCLUDED.state_changed_at"),
			"updated_at":       gorm.Expr("EXCLUDED.updated_at"),
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "post_reaction.reaction_version < EXCLUDED.reaction_version"},
		}},
	}).Create(&rows).Error
}
