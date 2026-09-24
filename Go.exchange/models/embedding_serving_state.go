package models

import "time"

// EmbeddingServingState stores the single runtime-selected post embedding
// version used by new recommendation requests.
type EmbeddingServingState struct {
	ID             uint      `gorm:"primaryKey;autoIncrement:false"`
	ServingVersion string    `gorm:"size:64;not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (EmbeddingServingState) TableName() string { return "embedding_serving_state" }
