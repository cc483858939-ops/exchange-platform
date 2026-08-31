package models

import "time"

const PostReactionLike int16 = 1

type PostReaction struct {
	UserID         uint      `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	PostID         uint      `json:"post_id" gorm:"primaryKey;autoIncrement:false;index:idx_post_reaction_post_reaction,priority:1"`
	Reaction       int16     `json:"reaction" gorm:"not null;index:idx_post_reaction_post_reaction,priority:2"`
	Liked          bool      `json:"liked" gorm:"not null;index"`
	Version        int64     `json:"reaction_version" gorm:"column:reaction_version;not null;default:0"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"not null;autoUpdateTime"`
	StateChangedAt time.Time `json:"state_changed_at" gorm:"column:state_changed_at;not null;index:idx_post_reaction_user_state,priority:3"`
}

func (PostReaction) TableName() string { return "post_reaction" }
