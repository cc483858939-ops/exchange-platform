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
	"Go.exchange/models"

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

type userBehaviorPair struct {
	UserID    uint
	ArticleID uint
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
	reader, err := eventing.NewKafkaReader(
		config.AppConfig.Kafka,
		config.AppConfig.Kafka.UserBehaviorTopic,
		config.AppConfig.Kafka.UserBehaviorGroupID,
	)
	if err != nil {
		log.Printf("[BehaviorProjection] create Kafka reader: %v", err)
		return
	}
	userBehaviorConsumers.Add(1)
	defer userBehaviorConsumers.Add(-1)
	defer reader.Close()
	if err := consumeUserBehaviorMessages(ctx, reader, applyUserBehaviorBatch); err != nil && ctx.Err() == nil {
		log.Printf("[BehaviorProjection] consumer stopped: %v", err)
	}
}

func consumeUserBehaviorMessages(
	ctx context.Context,
	reader userBehaviorMessageReader,
	applyBatch func([]kafka.Message) error,
) error {
	for {
		first, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] fetch Kafka message: %v", err)
			}
			return err
		}
		batch := collectUserBehaviorBatch(ctx, reader, first)
		if err := applyBatch(batch); err != nil {
			return fmt.Errorf("apply user behavior batch of %d messages: %w", len(batch), err)
		}
		if err := reader.CommitMessages(ctx, batch...); err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] commit Kafka batch: %v", err)
			}
			return err
		}
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

func applyUserBehaviorBatch(messages []kafka.Message) error {
	records := make([]userBehaviorEventRecord, 0, len(messages))
	seenEventIDs := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		record, err := decodeUserBehaviorEvent(message.Value)
		if err != nil {
			log.Printf("[BehaviorProjection] discard malformed or unsupported Kafka message: %v", err)
			continue
		}
		if _, exists := seenEventIDs[record.Envelope.ID]; exists {
			log.Printf("[BehaviorProjection] discard duplicate event in fetched batch: %s", record.Envelope.ID)
			continue
		}
		seenEventIDs[record.Envelope.ID] = struct{}{}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}
	return applyUserBehaviorRecords(records)
}

func decodeUserBehaviorEvent(raw []byte) (userBehaviorEventRecord, error) {
	event, err := eventing.DecodeEnvelope(raw)
	if err != nil {
		return userBehaviorEventRecord{}, err
	}
	if !isUserBehaviorEvent(event.Type) {
		return userBehaviorEventRecord{}, fmt.Errorf("unsupported user behavior event type %q", event.Type)
	}
	var payload eventing.UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return userBehaviorEventRecord{}, fmt.Errorf("decode user behavior payload: %w", err)
	}
	if payload.UserID == 0 || payload.ArticleID == 0 {
		return userBehaviorEventRecord{}, errors.New("user behavior payload requires user_id and article_id")
	}
	if (event.Type == eventing.EventTypeArticleLiked || event.Type == eventing.EventTypeArticleUnliked) &&
		payload.LikeVersion <= 0 {
		return userBehaviorEventRecord{}, errors.New("like behavior payload requires positive like_version")
	}
	return userBehaviorEventRecord{Envelope: event, Payload: payload}, nil
}

func isUserBehaviorEvent(eventType string) bool {
	return eventType == eventing.EventTypeArticleViewed ||
		eventType == eventing.EventTypeArticleLiked ||
		eventType == eventing.EventTypeArticleUnliked
}

// applyUserBehaviorEvent remains a single-event seam for existing operational and
// focused tests. Kafka consumption always uses applyUserBehaviorBatch.
func applyUserBehaviorEvent(event eventing.Envelope) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return applyUserBehaviorBatch([]kafka.Message{{Value: raw}})
}

// ConsumerInbox retention must be coordinated with Kafka retention, the replay
// window, and a future rebuild strategy before automatic cleanup is introduced.
func applyUserBehaviorRecords(records []userBehaviorEventRecord) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	consumerName := ""
	if config.AppConfig != nil {
		consumerName = strings.TrimSpace(config.AppConfig.Kafka.UserBehaviorGroupID)
	}
	if consumerName == "" {
		return errors.New("user behavior consumer group is not configured")
	}

	return global.Db.Transaction(func(tx *gorm.DB) error {
		eventIDs := make([]string, 0, len(records))
		for _, record := range records {
			eventIDs = append(eventIDs, record.Envelope.ID)
		}
		firstDelivery, err := eventing.MarkInboxProcessedBatch(tx, consumerName, eventIDs)
		if err != nil {
			return err
		}

		viewAggregates := aggregateUserBehaviorViews(records, firstDelivery)
		if err := bulkUpsertArticleViewBehavior(tx, viewAggregates); err != nil {
			return err
		}

		viewCountDeltas := aggregateArticleViewCountDeltas(records, firstDelivery)
		if err := incrementArticleViewCounts(tx, viewCountDeltas); err != nil {
			return err
		}

		reactions := collapseUserBehaviorReactions(records, firstDelivery)
		return bulkUpsertArticleReactions(tx, reactions)
	})
}

var incrementArticleViewCounts = bulkIncrementArticleViewCounts

func aggregateArticleViewCountDeltas(
	records []userBehaviorEventRecord,
	firstDelivery map[string]struct{},
) map[uint]int64 {
	deltas := make(map[uint]int64)
	for _, record := range records {
		if record.Envelope.Type != eventing.EventTypeArticleViewed {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok || record.Payload.ArticleID == 0 {
			continue
		}
		deltas[record.Payload.ArticleID]++
	}
	return deltas
}

func bulkIncrementArticleViewCounts(tx *gorm.DB, deltas map[uint]int64) error {
	if len(deltas) == 0 {
		return nil
	}

	articleIDs := make([]uint, 0, len(deltas))
	for articleID := range deltas {
		articleIDs = append(articleIDs, articleID)
	}
	sort.Slice(articleIDs, func(i, j int) bool {
		return articleIDs[i] < articleIDs[j]
	})

	var cases strings.Builder
	cases.WriteString("CASE id")
	args := make([]interface{}, 0, len(articleIDs)*2)
	for _, articleID := range articleIDs {
		cases.WriteString(" WHEN ? THEN ?")
		args = append(args, articleID, deltas[articleID])
	}
	cases.WriteString(" ELSE 0 END")

	return tx.Model(&models.Article{}).
		Where("id IN ?", articleIDs).
		UpdateColumn("view_count", gorm.Expr("view_count + ("+cases.String()+")", args...)).
		Error
}

func aggregateUserBehaviorViews(
	records []userBehaviorEventRecord,
	firstDelivery map[string]struct{},
) []userBehaviorViewAggregate {
	byPair := make(map[userBehaviorPair]userBehaviorViewAggregate)
	for _, record := range records {
		if record.Envelope.Type != eventing.EventTypeArticleViewed {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		key := userBehaviorPair{UserID: record.Payload.UserID, ArticleID: record.Payload.ArticleID}
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
		return keys[i].ArticleID < keys[j].ArticleID
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

func bulkUpsertArticleViewBehavior(tx *gorm.DB, aggregates []userBehaviorViewAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	rows := make([]models.ArticleBehavior, 0, len(aggregates))
	for _, aggregate := range aggregates {
		rows = append(rows, models.ArticleBehavior{
			Model:      gorm.Model{CreatedAt: updatedAt, UpdatedAt: updatedAt},
			UserID:     aggregate.Key.UserID,
			ArticleID:  aggregate.Key.ArticleID,
			Action:     "view",
			Count:      aggregate.Count,
			LastSeenAt: aggregate.LastSeenAt,
			Active:     true,
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("article_behaviors.count + EXCLUDED.count"),
			"last_seen_at": gorm.Expr("GREATEST(article_behaviors.last_seen_at, EXCLUDED.last_seen_at)"),
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
		if record.Envelope.Type != eventing.EventTypeArticleLiked &&
			record.Envelope.Type != eventing.EventTypeArticleUnliked {
			continue
		}
		if _, ok := firstDelivery[record.Envelope.ID]; !ok {
			continue
		}
		key := userBehaviorPair{UserID: record.Payload.UserID, ArticleID: record.Payload.ArticleID}
		candidate := userBehaviorReactionCandidate{
			Key:      key,
			Envelope: record.Envelope,
			Payload:  record.Payload,
			Liked:    record.Envelope.Type == eventing.EventTypeArticleLiked,
		}
		current, exists := byPair[key]
		if !exists || candidate.Payload.LikeVersion > current.Payload.LikeVersion {
			byPair[key] = candidate
			continue
		}
		if candidate.Payload.LikeVersion == current.Payload.LikeVersion && candidate.Liked != current.Liked {
			log.Printf(
				"[BehaviorProjection] conflicting equal like_version user=%d article=%d version=%d; keeping earliest Kafka event",
				key.UserID, key.ArticleID, candidate.Payload.LikeVersion,
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
		return keys[i].ArticleID < keys[j].ArticleID
	})
	result := make([]userBehaviorReactionCandidate, 0, len(keys))
	for _, key := range keys {
		result = append(result, byPair[key])
	}
	return result
}

func applyArticleReactionProjection(tx *gorm.DB, eventType string, payload eventing.UserBehaviorPayload, occurredAt time.Time) error {
	if eventType != eventing.EventTypeArticleLiked && eventType != eventing.EventTypeArticleUnliked {
		return nil
	}
	if payload.LikeVersion <= 0 {
		return nil
	}
	candidate := userBehaviorReactionCandidate{
		Key:      userBehaviorPair{UserID: payload.UserID, ArticleID: payload.ArticleID},
		Envelope: eventing.Envelope{Type: eventType, OccurredAt: occurredAt},
		Payload:  payload,
		Liked:    eventType == eventing.EventTypeArticleLiked,
	}
	return bulkUpsertArticleReactions(tx, []userBehaviorReactionCandidate{candidate})
}
func bulkUpsertArticleReactions(tx *gorm.DB, candidates []userBehaviorReactionCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC()
	rows := make([]models.ArticleReaction, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, models.ArticleReaction{
			UserID:         candidate.Key.UserID,
			ArticleID:      candidate.Key.ArticleID,
			Reaction:       models.ArticleReactionLike,
			Liked:          candidate.Liked,
			Version:        candidate.Payload.LikeVersion,
			UpdatedAt:      updatedAt,
			StateChangedAt: userBehaviorOccurredAt(candidate.Envelope),
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"reaction":         gorm.Expr("EXCLUDED.reaction"),
			"liked":            gorm.Expr("EXCLUDED.liked"),
			"reaction_version": gorm.Expr("EXCLUDED.reaction_version"),
			"state_changed_at": gorm.Expr("EXCLUDED.state_changed_at"),
			"updated_at":       gorm.Expr("EXCLUDED.updated_at"),
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "article_reaction.reaction_version < EXCLUDED.reaction_version"},
		}},
	}).Create(&rows).Error
}
