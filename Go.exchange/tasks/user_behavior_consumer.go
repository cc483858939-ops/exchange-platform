package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const articleBehaviorRetentionLimit = 200
const userBehaviorConsumerRetryDelay = 2 * time.Second

func startUserBehaviorProjectionConsumer(ctx context.Context, wg *sync.WaitGroup) {
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
	reader, err := eventing.NewKafkaReader(config.AppConfig.Kafka, config.AppConfig.Kafka.UserBehaviorTopic, config.AppConfig.Kafka.UserBehaviorGroupID)
	if err != nil {
		log.Printf("[BehaviorProjection] create Kafka reader: %v", err)
		return
	}
	userBehaviorConsumers.Add(1)
	defer userBehaviorConsumers.Add(-1)
	defer reader.Close()
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] fetch Kafka message: %v", err)
			}
			return
		}
		event, err := eventing.DecodeEnvelope(message.Value)
		if err != nil {
			log.Printf("[BehaviorProjection] discard malformed Kafka message: %v", err)
		} else if isUserBehaviorEvent(event.Type) {
			if err := applyUserBehaviorEvent(event); err != nil {
				log.Printf("[BehaviorProjection] apply event: %v", err)
				return
			}
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			if ctx.Err() == nil {
				log.Printf("[BehaviorProjection] commit Kafka message: %v", err)
			}
			return
		}
	}
}

func isUserBehaviorEvent(eventType string) bool {
	return eventType == eventing.EventTypeArticleViewed || eventType == eventing.EventTypeArticleLiked || eventType == eventing.EventTypeArticleUnliked
}

func applyUserBehaviorEvent(event eventing.Envelope) error {
	var payload eventing.UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode user behavior payload: %w", err)
	}
	if payload.UserID == 0 || payload.ArticleID == 0 {
		return fmt.Errorf("user behavior payload requires user_id and article_id")
	}
	return global.Db.Transaction(func(tx *gorm.DB) error {
		firstDelivery, err := eventing.MarkInboxProcessed(tx, config.AppConfig.Kafka.UserBehaviorGroupID, event.ID)
		if err != nil || !firstDelivery {
			return err
		}
		if err := applyArticleReactionProjection(tx, event.Type, payload); err != nil {
			return err
		}
		now := event.OccurredAt
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if event.Type == eventing.EventTypeArticleLiked || event.Type == eventing.EventTypeArticleUnliked {
			return applyLikeBehaviorProjection(tx, event.Type, payload, now)
		}
		return applyViewBehaviorProjection(tx, payload, now)
	})
}

func applyLikeBehaviorProjection(tx *gorm.DB, eventType string, payload eventing.UserBehaviorPayload, now time.Time) error {
	if payload.LikeVersion <= 0 {
		return fmt.Errorf("like behavior requires a positive like_version")
	}
	liked := eventType == eventing.EventTypeArticleLiked
	count := int64(0)
	if liked {
		count = 1
	}
	behavior := models.ArticleBehavior{
		UserID: payload.UserID, ArticleID: payload.ArticleID, Action: "like",
		Count: count, LastSeenAt: now, Active: liked, BehaviorVersion: payload.LikeVersion,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count": count, "last_seen_at": now, "active": liked,
			"behavior_version": payload.LikeVersion, "updated_at": now,
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Lt{Column: clause.Column{Table: "article_behaviors", Name: "behavior_version"}, Value: payload.LikeVersion},
		}},
	}).Create(&behavior).Error; err != nil {
		return err
	}
	if liked {
		return enforceBehaviorProjectionRetention(tx, payload.UserID, "like")
	}
	return nil
}

func applyViewBehaviorProjection(tx *gorm.DB, payload eventing.UserBehaviorPayload, now time.Time) error {
	behavior := models.ArticleBehavior{
		UserID: payload.UserID, ArticleID: payload.ArticleID, Action: "view",
		Count: 1, LastSeenAt: now, Active: true,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("article_behaviors.count + ?", 1),
			"last_seen_at": now, "active": true, "updated_at": now,
		}),
	}).Create(&behavior).Error; err != nil {
		return err
	}
	return enforceBehaviorProjectionRetention(tx, payload.UserID, "view")
}

func applyArticleReactionProjection(tx *gorm.DB, eventType string, payload eventing.UserBehaviorPayload) error {
	if payload.LikeVersion <= 0 || (eventType != eventing.EventTypeArticleLiked && eventType != eventing.EventTypeArticleUnliked) {
		return nil
	}
	liked := eventType == eventing.EventTypeArticleLiked
	reaction := models.ArticleReaction{
		UserID: payload.UserID, ArticleID: payload.ArticleID,
		Reaction: models.ArticleReactionLike, Liked: liked, Version: payload.LikeVersion,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"reaction": models.ArticleReactionLike, "liked": liked,
			"reaction_version": payload.LikeVersion, "updated_at": time.Now().UTC(),
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Lt{Column: clause.Column{Table: "article_reaction", Name: "reaction_version"}, Value: payload.LikeVersion},
		}},
	}).Create(&reaction).Error
}

func enforceBehaviorProjectionRetention(tx *gorm.DB, userID uint, action string) error {
	var behaviors []models.ArticleBehavior
	if err := tx.Select("id,last_seen_at,active").
		Where("user_id = ? AND action = ? AND active = ?", userID, action, true).
		Find(&behaviors).Error; err != nil {
		return err
	}
	sort.Slice(behaviors, func(i, j int) bool {
		if !behaviors[i].LastSeenAt.Equal(behaviors[j].LastSeenAt) {
			return behaviors[i].LastSeenAt.After(behaviors[j].LastSeenAt)
		}
		return behaviors[i].ID > behaviors[j].ID
	})
	if len(behaviors) <= articleBehaviorRetentionLimit {
		return nil
	}
	ids := make([]uint, 0, len(behaviors)-articleBehaviorRetentionLimit)
	for _, behavior := range behaviors[articleBehaviorRetentionLimit:] {
		ids = append(ids, behavior.ID)
	}
	return tx.Model(&models.ArticleBehavior{}).Where("id IN ?", ids).Update("active", false).Error
}
