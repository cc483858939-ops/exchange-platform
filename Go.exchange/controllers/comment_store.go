package controllers

import (
	"errors"
	"log"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errCommentForbidden          = errors.New("comment is not owned by authenticated user")
	errCommentCountConsistency   = errors.New("comment count consistency error")
	incrementArticleCommentCount = func(tx *gorm.DB, articleID uint) (int64, error) {
		result := tx.Model(&models.Article{}).
			Where("id = ?", articleID).
			UpdateColumn("comment_count", gorm.Expr("comment_count + 1"))
		return result.RowsAffected, result.Error
	}
	decrementArticleCommentCount = func(tx *gorm.DB, articleID uint) (int64, error) {
		result := tx.Model(&models.Article{}).
			Where("id = ? AND comment_count > 0", articleID).
			UpdateColumn("comment_count", gorm.Expr("comment_count - 1"))
		return result.RowsAffected, result.Error
	}
	invalidateCommentArticleDetailCache = func(articleID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidateArticleDetailCacheByID(articleID)
	}
)

func upsertReplyArticleBehavior(tx *gorm.DB, userID, articleID uint, occurredAt time.Time) error {
	if userID == 0 || articleID == 0 {
		return errors.New("reply behavior requires user and article")
	}
	behavior := models.ArticleBehavior{
		Model:  gorm.Model{CreatedAt: occurredAt, UpdatedAt: occurredAt},
		UserID: userID, ArticleID: articleID, Action: ArticleBehaviorActionReply,
		Count: 1, LastSeenAt: occurredAt, Active: true,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "article_id"}, {Name: "action"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("article_behaviors.count + EXCLUDED.count"),
			"last_seen_at": gorm.Expr("GREATEST(article_behaviors.last_seen_at, EXCLUDED.last_seen_at)"),
			"active":       true,
			"updated_at":   occurredAt,
		}),
	}).Create(&behavior).Error
}

func createCommentWithCount(articleID, userID uint, content string) (models.Comment, error) {
	if global.Db == nil {
		return models.Comment{}, errors.New("database is not initialized")
	}

	comment := models.Comment{ArticleID: articleID, UserID: userID, Content: content}
	occurredAt := time.Now().UTC()
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var article models.Article
		if err := tx.
			Select("id").
			Scopes(func(query *gorm.DB) *gorm.DB { return publicArticleScope(query, occurredAt) }).
			First(&article, articleID).Error; err != nil {
			return err
		}
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		rowsAffected, err := incrementArticleCommentCount(tx, articleID)
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return errCommentCountConsistency
		}
		if err := upsertReplyArticleBehavior(tx, userID, articleID, occurredAt); err != nil {
			return err
		}
		if err := recommendation.InvalidateProfiles(tx, []uint{userID}, "reply_created", occurredAt); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return models.Comment{}, err
	}
	return comment, nil
}
func deleteCommentWithCount(commentID, userID uint) (models.Comment, error) {
	if global.Db == nil {
		return models.Comment{}, errors.New("database is not initialized")
	}

	var comment models.Comment
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&comment, commentID).Error; err != nil {
			return err
		}
		if comment.UserID != userID {
			return errCommentForbidden
		}
		result := tx.Where("id = ? AND user_id = ?", commentID, userID).Delete(&models.Comment{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		rowsAffected, err := decrementArticleCommentCount(tx, comment.ArticleID)
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return errCommentCountConsistency
		}
		return nil
	})
	if err != nil {
		return models.Comment{}, err
	}
	return comment, nil
}

func invalidateCommentArticleCaches(articleID uint) {
	if err := invalidateCommentArticleDetailCache(articleID); err != nil {
		log.Printf("[Comment] invalidate article detail cache for %d: %v", articleID, err)
	}
}
