package models

import "time"

const ArticleReactionLike int16 = 1

type ArticleReaction struct {
	UserID    uint      `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	ArticleID uint      `json:"article_id" gorm:"primaryKey;autoIncrement:false;index:idx_article_reaction_article_reaction,priority:1"`
	Reaction  int16     `json:"reaction" gorm:"not null;index:idx_article_reaction_article_reaction,priority:2"`
	Liked     bool      `json:"liked" gorm:"not null;default:true;index"`
	Version   int64     `json:"reaction_version" gorm:"column:reaction_version;not null;default:0"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null;autoUpdateTime"`
}

func (ArticleReaction) TableName() string {
	return "article_reaction"
}
