package models

import "time"

// ConsumerInbox deduplicates at-least-once Kafka delivery per consumer group.
type ConsumerInbox struct {
	ConsumerName string    `json:"consumer_name" gorm:"primaryKey;size:128"`
	EventID      string    `json:"event_id" gorm:"primaryKey;size:36"`
	ProcessedAt  time.Time `json:"processed_at" gorm:"not null"`
}
