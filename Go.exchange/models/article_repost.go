package models

import "time"

// PostRepost records one user's active repost relation to a canonical Post.
// It is intentionally not a gorm.Model: undoing a repost hard-deletes this row.
type PostRepost struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null"`
	PostID    uint      `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
}

func (PostRepost) TableName() string { return "post_reposts" }
