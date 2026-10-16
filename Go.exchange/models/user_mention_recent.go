package models

import "time"

// UserMentionRecent stores only the latest mention time per user pair.
type UserMentionRecent struct {
	UserID          uint      `gorm:"primaryKey;not null"`
	MentionedUserID uint      `gorm:"primaryKey;not null"`
	LastMentionedAt time.Time `gorm:"not null"`
}

func (UserMentionRecent) TableName() string { return "user_mention_recents" }
