package models

import "time"

// UserRecoProfileDirty is the durable per-user invalidation and retry queue.
type UserRecoProfileDirty struct {
	UserID        uint      `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	DirtyVersion  int64     `json:"dirty_version" gorm:"not null;default:1"`
	DirtyAt       time.Time `json:"dirty_at" gorm:"not null"`
	Reason        string    `json:"reason" gorm:"size:64;not null;default:''"`
	Attempts      int       `json:"attempts" gorm:"not null;default:0"`
	NextAttemptAt time.Time `json:"next_attempt_at" gorm:"not null;index:idx_user_reco_profile_dirty_due,priority:1"`
	LastError     string    `json:"last_error" gorm:"size:512;not null;default:''"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"not null"`
}

func (UserRecoProfileDirty) TableName() string { return "user_reco_profile_dirty" }
