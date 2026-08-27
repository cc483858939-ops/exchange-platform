package models

import "time"

// ArticleRepost records one user's active repost relation to a canonical article.
// It is intentionally not a gorm.Model: undoing a repost hard-deletes this row.
type ArticleRepost struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null"`
	ArticleID uint      `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
}
