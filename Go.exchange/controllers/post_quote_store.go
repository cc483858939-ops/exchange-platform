package controllers

import (
	"errors"

	"Go.exchange/models"

	"gorm.io/gorm"
)

var (
	errPostQuoteCountConsistency = errors.New("post quote count consistency error")

	incrementPostQuoteCount = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Model(&models.Post{}).
			Where("id = ?", postID).
			UpdateColumn("quote_count", gorm.Expr("quote_count + 1"))
		return result.RowsAffected, result.Error
	}

	decrementPostQuoteCount = func(tx *gorm.DB, postID uint) (int64, error) {
		result := tx.Unscoped().
			Model(&models.Post{}).
			Where("id = ? AND quote_count > 0", postID).
			UpdateColumn("quote_count", gorm.Expr("quote_count - 1"))
		return result.RowsAffected, result.Error
	}
)
