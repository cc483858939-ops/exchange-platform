package models

import (
	"time"

	"gorm.io/gorm"
)

type Article struct {
	gorm.Model
	Title     string     `binding:"required"`
	Content   string     `binding:"required"`
	Preview   string     `binding:"required"`
	ExpiredAt *time.Time `json:"expired_at"`
	LikeCount int64      `json:"like_count" gorm:"default:0"`
}
