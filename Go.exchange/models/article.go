package models

import (
	"time"

	"gorm.io/gorm"
)

type Article struct {
	gorm.Model
	Title   string `json:"title" binding:"required"`
	Content string `json:"content" binding:"required"`
	Preview string `json:"preview" binding:"required"`
	// 以下字段由异步 AI 分析链路回填，客户端创建文章时不允许直接写入。
	Summary   string     `json:"summary" gorm:"type:text"`
	Tags      []string   `json:"tags" gorm:"type:json;serializer:json"`
	Category  string     `json:"category" gorm:"size:64"`
	Status    string     `json:"status" gorm:"size:32;default:pending;not null"`
	ExpiredAt *time.Time `json:"expired_at"`
	LikeCount int64      `json:"like_count" gorm:"default:0"`
}
