package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// UserRecoProfile is the serving projection produced by the nearline
// materializer. A nil vector is a valid cold-start/one-sided profile.
type UserRecoProfile struct {
	UserID                  uint             `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	ProfileVersion          string           `json:"profile_version" gorm:"size:64;not null"`
	ProfileConfigHash       string           `json:"profile_config_hash" gorm:"size:32;not null"`
	EmbeddingVersion        string           `json:"embedding_version" gorm:"size:64;not null"`
	Dimensions              int              `json:"dimensions" gorm:"not null;default:0"`
	PositiveVector          *pgvector.Vector `json:"-" gorm:"type:vector"`
	NegativeVector          *pgvector.Vector `json:"-" gorm:"type:vector"`
	NegativeEvidence        float64          `json:"negative_evidence" gorm:"not null;default:0"`
	PositiveSignalCount     int              `json:"positive_signal_count" gorm:"not null;default:0"`
	NegativeSignalCount     int              `json:"negative_signal_count" gorm:"not null;default:0"`
	PersonalizedSignalCount int              `json:"personalized_signal_count" gorm:"not null;default:0"`
	ComputedAt              time.Time        `json:"computed_at" gorm:"not null"`
	NextRebuildAt           time.Time        `json:"next_rebuild_at" gorm:"not null;index:idx_user_reco_profiles_next_rebuild"`
	UpdatedAt               time.Time        `json:"updated_at" gorm:"not null"`
}

func (UserRecoProfile) TableName() string { return "user_reco_profiles" }
