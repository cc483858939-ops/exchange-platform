package models

import "time"

// KafkaDLQReplay records the at-least-once publish attempt for an operator replay.
type KafkaDLQReplay struct {
	ID string `gorm:"primaryKey;size:36"`

	DLQTopic     string `gorm:"not null;size:255"`
	DLQPartition int    `gorm:"not null"`
	DLQOffset    int64  `gorm:"not null"`

	Consumer string `gorm:"not null;size:128"`

	SourceTopic     string `gorm:"not null;size:255"`
	SourcePartition int    `gorm:"not null"`
	SourceOffset    int64  `gorm:"not null"`

	EventID   string `gorm:"not null;size:128;default:''"`
	ErrorCode string `gorm:"not null;size:128"`
	Status    string `gorm:"not null;size:16;index"`

	StartedAt   time.Time `gorm:"not null;index"`
	CompletedAt *time.Time

	Error string `gorm:"type:text;not null;default:''"`
}

func (KafkaDLQReplay) TableName() string { return "kafka_dlq_replays" }
