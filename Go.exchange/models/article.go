package models

import (
	"time"

	"gorm.io/gorm"
)

type Article struct {
	gorm.Model
	AuthorID      uint   `json:"-" gorm:"not null"`
	Author        User   `json:"-" gorm:"foreignKey:AuthorID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Title         string `json:"title" binding:"required"`
	Content       string `json:"content" binding:"required"`
	Preview       string `json:"preview" binding:"required"`
	CoverImageURL string `json:"cover_image_url" gorm:"size:512"`
	// 以下字段由异步 AI 分析链路回填，客户端创建文章时不允许直接写入。
	Summary          string     `json:"summary" gorm:"type:text"`
	Tags             []string   `json:"tags" gorm:"type:json;serializer:json"`
	Category         string     `json:"category" gorm:"size:64"`
	PublicationState string     `json:"publication_state" gorm:"size:32;not null;default:published;index:idx_articles_recommendation,priority:1"`
	AnalysisState    string     `json:"analysis_state" gorm:"size:32;not null;default:pending;index:idx_articles_recommendation,priority:2"`
	AnalysisVersion  string     `json:"analysis_version" gorm:"size:64;not null;default:v1"`
	PublishedAt      *time.Time `json:"published_at"`
	ExpiredAt        *time.Time `json:"expired_at" gorm:"index:idx_articles_recommendation,priority:3"`
	LikeCount        int64      `json:"like_count" gorm:"default:0"`
	CommentCount     int64      `json:"comment_count" gorm:"not null;default:0"`
	LikeSyncVersion  int64      `json:"like_sync_version" gorm:"not null;default:0"`
}
