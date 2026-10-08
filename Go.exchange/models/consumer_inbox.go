package models

import "time"

// ConsumerInboxEventIDMaxLength matches PostgreSQL varchar(128), measured in
// Unicode characters. Preserve legacy opaque keys, including non-ASCII IDs.
const ConsumerInboxEventIDMaxLength = 128

// ConsumerInbox deduplicates at-least-once Kafka delivery per consumer group.
type ConsumerInbox struct {
	ConsumerName string    `json:"consumer_name" gorm:"primaryKey;size:128"`
	EventID      string    `json:"event_id" gorm:"primaryKey;size:128"`
	ProcessedAt  time.Time `json:"processed_at" gorm:"not null"`
}
