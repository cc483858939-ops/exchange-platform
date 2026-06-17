package models

import (
	"time"

	"gorm.io/gorm"
)

type ArticleBehavior struct {
	gorm.Model
	UserID     uint      `json:"user_id" gorm:"not null;index;uniqueIndex:idx_article_behavior_user_article_action;index:idx_article_behavior_user_action_active_seen,priority:1"`
	ArticleID  uint      `json:"article_id" gorm:"not null;index;uniqueIndex:idx_article_behavior_user_article_action"`
	Action     string    `json:"action" gorm:"size:32;not null;index;uniqueIndex:idx_article_behavior_user_article_action;index:idx_article_behavior_user_action_active_seen,priority:2"`
	Count      int64     `json:"count" gorm:"not null;default:0"`
	LastSeenAt time.Time `json:"last_seen_at" gorm:"not null;index:idx_article_behavior_user_action_active_seen,priority:4,sort:desc"`
	Active     bool      `json:"active" gorm:"not null;default:true;index:idx_article_behavior_user_action_active_seen,priority:3"`
}
