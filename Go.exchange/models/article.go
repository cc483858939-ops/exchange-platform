package models

import (
	"time"

	"gorm.io/gorm"
)

type Article struct {
	gorm.Model
	AuthorID         uint       `json:"-" gorm:"not null"`
	Author           User       `json:"-" gorm:"foreignKey:AuthorID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Title            string     `json:"title" binding:"required"`
	Content          string     `json:"content" binding:"required"`
	Preview          string     `json:"preview" binding:"required"`
	CoverImageURL    string     `json:"cover_image_url" gorm:"size:512"`
	PublicationState string     `json:"publication_state" gorm:"size:32;not null;default:published"`
	PublishedAt      *time.Time `json:"published_at"`
	ExpiredAt        *time.Time `json:"expired_at" gorm:"index"`
	LikeCount        int64      `json:"like_count" gorm:"default:0"`
	CommentCount     int64      `json:"comment_count" gorm:"not null;default:0"`
	ViewCount        int64      `json:"view_count" gorm:"not null;default:0"`
	LikeSyncVersion  int64      `json:"like_sync_version" gorm:"not null;default:0"`
}
