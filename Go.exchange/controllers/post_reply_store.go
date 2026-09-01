package controllers

import (
	"errors"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errPostReplyCountConsistency = errors.New("post reply count consistency error")
	incrementPostReplyCount      = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Model(&models.Post{}).
			Where("id = ?", postID).
			UpdateColumn("reply_count", gorm.Expr("reply_count + 1"))
		return result.RowsAffected, result.Error
	}
	decrementPostReplyCount = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Unscoped().Model(&models.Post{}).
			Where("id = ? AND reply_count > 0", postID).
			UpdateColumn("reply_count", gorm.Expr("reply_count - 1"))
		return result.RowsAffected, result.Error
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
