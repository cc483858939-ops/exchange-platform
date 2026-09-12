package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Post is the canonical content root. Replies, quotes, and short posts all
// share this table.
type Post struct {
	gorm.Model

	AuthorID uint `json:"-" gorm:"not null"`
	Author   User `json:"-" gorm:"foreignKey:AuthorID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`

	Content  string `json:"content" gorm:"type:text;not null"`
	Language string `json:"language" gorm:"size:8;not null;default:und"`

	// ClientPublishID and ClientPublishFingerprint are internal write-side
	// identity fields used to make a client publish operation replay-safe.
	ClientPublishID          *uuid.UUID `json:"-" gorm:"type:uuid"`
	ClientPublishFingerprint *string    `json:"-" gorm:"type:char(64)"`

	ReplyToPostID *uint `json:"reply_to_post_id"`
	ReplyToPost   *Post `json:"-" gorm:"foreignKey:ReplyToPostID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`

	QuotePostID *uint `json:"quote_post_id"`
	QuotePost   *Post `json:"-" gorm:"foreignKey:QuotePostID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`

	ConversationID *uint `json:"conversation_id"`

	Visibility string `json:"visibility" gorm:"size:16;not null;default:public"`

	LikeCount       int64 `json:"like_count" gorm:"not null;default:0"`
	ReplyCount      int64 `json:"reply_count" gorm:"not null;default:0"`
	ViewCount       int64 `json:"view_count" gorm:"not null;default:0"`
	LikeSyncVersion int64 `json:"like_sync_version" gorm:"not null;default:0"`
}

func (Post) TableName() string { return "posts" }
