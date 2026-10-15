package models

// UserOnboardingInterest stores one enabled topic slug selected by a user.
type UserOnboardingInterest struct {
	UserID    uint   `gorm:"primaryKey;autoIncrement:false"`
	TopicSlug string `gorm:"type:varchar(64);primaryKey"`
}

func (UserOnboardingInterest) TableName() string { return "user_onboarding_interests" }
