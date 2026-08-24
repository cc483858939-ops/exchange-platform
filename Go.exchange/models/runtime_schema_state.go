package models

import "time"

// RuntimeSchemaState is the single-row compatibility contract published by
// the migration job. Runtime processes never mutate this row.
type RuntimeSchemaState struct {
	ID                 uint      `gorm:"primaryKey"`
	CurrentVersion     int64     `gorm:"not null"`
	CompatibilityFloor int64     `gorm:"not null"`
	AppliedAt          time.Time `gorm:"not null"`
	ReleaseRevision    string    `gorm:"size:128;not null"`
}

func (RuntimeSchemaState) TableName() string { return "runtime_schema_state" }
