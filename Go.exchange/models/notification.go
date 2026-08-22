package models

import "time"

const (
	NotificationTypePostLiked    = "post_liked"
	NotificationTypePostReplied  = "post_replied"
	NotificationTypeUserFollowed = "user_followed"
)

// Notification is a derived, idempotent read model projected from activity
// events. It intentionally has no relationship to the source Outbox row.
type Notification struct {
	ID            uint       `gorm:"primaryKey"`
	RecipientID   uint       `gorm:"not null"`
	ActorID       uint       `gorm:"not null"`
	Type          string     `gorm:"column:notification_type;size:32;not null"`
	ArticleID     *uint      `gorm:"column:article_id"`
	CommentID     *uint      `gorm:"column:comment_id"`
	DedupeKey     string     `gorm:"size:192;not null"`
	SourceVersion int64      `gorm:"not null;default:0"`
	ActivityAt    time.Time  `gorm:"not null"`
	ReadAt        *time.Time `gorm:"column:read_at"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}
