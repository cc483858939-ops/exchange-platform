package models

import "time"

// OutboxEvent is the immutable CDC source row. The complete canonical event
// envelope is stored in Message and is routed by Topic/PartitionKey.
type OutboxEvent struct {
	ID            string    `json:"id" gorm:"type:uuid;primaryKey"`
	Topic         string    `json:"topic" gorm:"size:255;not null"`
	PartitionKey  string    `json:"partition_key" gorm:"size:255;not null"`
	EventType     string    `json:"event_type" gorm:"size:128;not null"`
	SchemaVersion int       `json:"schema_version" gorm:"not null"`
	AggregateType string    `json:"aggregate_type" gorm:"size:64;not null"`
	AggregateID   string    `json:"aggregate_id" gorm:"size:128;not null"`
	Message       string    `json:"message" gorm:"type:jsonb;not null"`
	OccurredAt    time.Time `json:"occurred_at" gorm:"not null"`
	CreatedAt     time.Time `json:"created_at" gorm:"not null"`
}
