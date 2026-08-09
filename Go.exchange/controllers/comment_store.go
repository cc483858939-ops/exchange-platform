package controllers

import (
	"errors"
	"log"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
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
	invalidateCommentArticleListCache = func() error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidateArticleListCache()
	}
	invalidateCommentArticleDetailCache = func(articleID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidateArticleDetailCacheByID(articleID)
	}
)

func createCommentWithCount(articleID, userID uint, content string) (models.Comment, error) {
	if global.Db == nil {
		return models.Comment{}, errors.New("database is not initialized")
	}

	comment := models.Comment{ArticleID: articleID, UserID: userID, Content: content}
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var article models.Article
		if err := tx.
			Select("id").
			Scopes(func(query *gorm.DB) *gorm.DB { return visibleArticleScope(query, time.Now()) }).
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
	if err := invalidateCommentArticleListCache(); err != nil {
		log.Printf("[Comment] invalidate article list cache: %v", err)
	}
	if err := invalidateCommentArticleDetailCache(articleID); err != nil {
		log.Printf("[Comment] invalidate article detail cache for %d: %v", articleID, err)
	}
}
