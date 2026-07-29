package models

import "time"

// OutboxEvent makes database changes and Kafka publication atomic from the
// caller's point of view. The relay can safely publish an event more than once.
type OutboxEvent struct {
	ID              string     `json:"id" gorm:"primaryKey;size:36"`
	AggregateType   string     `json:"aggregate_type" gorm:"size:64;not null"`
	AggregateID     string     `json:"aggregate_id" gorm:"size:128;not null"`
	EventType       string     `json:"event_type" gorm:"size:128;not null;index:idx_outbox_events_pending,priority:1"`
	SchemaVersion   int        `json:"schema_version" gorm:"not null;default:1"`
	Payload         string     `json:"payload" gorm:"type:jsonb;not null"`
	OccurredAt      time.Time  `json:"occurred_at" gorm:"not null"`
	PublishedAt     *time.Time `json:"published_at" gorm:"index:idx_outbox_events_pending,priority:2"`
	PublishAttempts int        `json:"publish_attempts" gorm:"not null;default:0"`
	LastError       string     `json:"last_error" gorm:"type:text"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
