package models

import "time"

// MigrationStep is migration-owned metadata, not an API/worker prerequisite.
type MigrationStep struct {
	ID                 string    `gorm:"primaryKey;size:128"`
	SQLChecksum        string    `gorm:"size:64;not null"`
	CatalogFingerprint string    `gorm:"size:32;not null"`
	AppliedAt          time.Time `gorm:"not null"`
}
