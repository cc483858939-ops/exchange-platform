package models

import "time"

const (
	UserOnboardingStepAvatar      = "avatar"
	UserOnboardingStepDisplayName = "display_name"
	UserOnboardingStepInterests   = "interests"
	UserOnboardingStepFollowUsers = "follow_users"
)

// UserOnboarding stores the resumable onboarding step and terminal completion time.
type UserOnboarding struct {
	UserID      uint       `gorm:"primaryKey;autoIncrement:false"`
	CurrentStep string     `gorm:"type:varchar(32);not null;default:avatar"`
	CompletedAt *time.Time `gorm:"type:timestamptz"`
	CreatedAt   time.Time  `gorm:"not null;default:now();autoCreateTime"`
	UpdatedAt   time.Time  `gorm:"not null;default:now();autoUpdateTime"`
}

func (UserOnboarding) TableName() string { return "user_onboardings" }
