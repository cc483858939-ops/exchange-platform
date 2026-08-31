package controllers

import (
	"errors"
	"log"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errReplyForbidden        = errors.New("reply is not owned by authenticated user")
	errReplyCountConsistency = errors.New("reply count consistency error")
	incrementPostReplyCount  = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Model(&models.Post{}).
			Where("id = ?", postID).
			UpdateColumn("reply_count", gorm.Expr("reply_count + 1"))
		return result.RowsAffected, result.Error
	}
	decrementPostReplyCount = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Model(&models.Post{}).
			Where("id = ? AND reply_count > 0", postID).
			UpdateColumn("reply_count", gorm.Expr("reply_count - 1"))
		return result.RowsAffected, result.Error
	}
	invalidateReplyPostDetailCache = func(postID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidatePostDetailCacheByID(postID)
	}
)

func upsertReplyPostBehavior(tx *gorm.DB, userID, postID uint, occurredAt time.Time) error {
	if userID == 0 || postID == 0 {
		return errors.New("reply behavior requires user and post")
	}
	behavior := models.PostBehavior{
		Model:  gorm.Model{CreatedAt: occurredAt, UpdatedAt: occurredAt},
		UserID: userID, PostID: postID, Action: PostBehaviorActionReply,
		Count: 1, LastSeenAt: occurredAt, Active: true,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "post_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("post_behaviors.count + EXCLUDED.count"),
			"last_seen_at": gorm.Expr("GREATEST(post_behaviors.last_seen_at, EXCLUDED.last_seen_at)"),
			"active":       true,
			"updated_at":   occurredAt,
		}),
	}).Create(&behavior).Error
}

func createReplyWithCount(postID, userID uint, content string) (models.Post, error) {
	if global.Db == nil {
		return models.Post{}, errors.New("database is not initialized")
	}

	reply := models.Post{AuthorID: userID, Content: content, ReplyToPostID: &postID, Visibility: "public"}
	occurredAt := time.Now().UTC()
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var parent models.Post
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Scopes(func(query *gorm.DB) *gorm.DB { return publicPostScope(query, occurredAt) }).
			First(&parent, postID).Error; err != nil {
			return err
		}
		rootID := parent.ID
		if parent.ConversationID != nil && *parent.ConversationID != 0 {
			rootID = *parent.ConversationID
		}
		reply.ConversationID = &rootID
		reply.CreatedAt = occurredAt
		reply.UpdatedAt = occurredAt
		if err := tx.Create(&reply).Error; err != nil {
			return err
		}
		rowsAffected, err := incrementPostReplyCount(tx, parent.ID)
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return errReplyCountConsistency
		}
		if err := upsertReplyPostBehavior(tx, userID, rootID, occurredAt); err != nil {
			return err
		}
		if err := recommendation.InvalidateProfiles(tx, []uint{userID}, "reply_created", occurredAt); err != nil {
			return err
		}
		activity, err := eventing.NewReplyCreatedEnvelope(uuid.NewString(), eventing.ReplyCreatedPayload{
			ReplyPostID: reply.ID, ParentPostID: parent.ID, ConversationID: rootID,
			ActorID: userID, ParentAuthorID: parent.AuthorID, CreatedAt: occurredAt,
		})
		if err != nil {
			return err
		}
		if err := addConfiguredActivityOutboxEvent(tx, activity); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return models.Post{}, err
	}
	return reply, nil
}
func deleteReplyWithCount(replyID, userID uint) (models.Post, error) {
	if global.Db == nil {
		return models.Post{}, errors.New("database is not initialized")
	}

	var reply models.Post
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&reply, replyID).Error; err != nil {
			return err
		}
		if reply.AuthorID != userID || reply.ReplyToPostID == nil {
			return errReplyForbidden
		}
		result := tx.Where("id = ? AND author_id = ? AND deleted_at IS NULL", replyID, userID).Delete(&models.Post{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		rowsAffected, err := decrementPostReplyCount(tx, *reply.ReplyToPostID)
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return errReplyCountConsistency
		}
		return nil
	})
	if err != nil {
		return models.Post{}, err
	}
	return reply, nil
}

func invalidateReplyPostCaches(postID uint) {
	if err := invalidateReplyPostDetailCache(postID); err != nil {
		log.Printf("[Reply] invalidate post detail cache for %d: %v", postID, err)
	}
}
