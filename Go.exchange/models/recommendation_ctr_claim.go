package models

import "time"

// RecommendationCTRClaim is the durable business idempotency ledger for CTR.
// event_id remains the transport idempotency key in ConsumerInbox. A request
// can produce at most one impression and one click for a given user and Post.
// The click path also claims an impression (a click proves an exposure).
type RecommendationCTRClaim struct {
	UserID    uint      `gorm:"primaryKey;not null"`
	RequestID string    `gorm:"type:uuid;primaryKey;not null"`
	PostID    uint      `gorm:"primaryKey;not null"`
	EventType string    `gorm:"size:16;primaryKey;not null"`
	CreatedAt time.Time `gorm:"not null"`
}
