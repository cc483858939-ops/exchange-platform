package models

import "time"

const (
	RecommendationEventTypeImpression = "impression"
	RecommendationEventTypeClick      = "click"
)

// RecommendationEvent is the durable, server-attributed fact used to rebuild
// recommendation telemetry projections.
type RecommendationEvent struct {
	EventID          string    `json:"event_id" gorm:"primaryKey;type:uuid"`
	UserID           uint      `json:"user_id" gorm:"not null;index:idx_recommendation_events_user_occurred,priority:1"`
	RequestID        string    `json:"request_id" gorm:"type:uuid;not null;uniqueIndex:idx_recommendation_events_business,priority:1"`
	ArticleID        uint      `json:"article_id" gorm:"not null;uniqueIndex:idx_recommendation_events_business,priority:2;index:idx_recommendation_events_article_occurred,priority:1"`
	EventType        string    `json:"event_type" gorm:"size:16;not null;uniqueIndex:idx_recommendation_events_business,priority:3;check:chk_recommendation_event_type,event_type IN ('impression','click')"`
	Scene            string    `json:"scene" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:1"`
	Position         int       `json:"position" gorm:"not null;check:chk_recommendation_event_position,position > 0"`
	RankerVersion    string    `json:"ranker_version" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:2"`
	RankerConfigHash string    `json:"ranker_config_hash" gorm:"size:32;not null;index:idx_recommendation_events_dimension_occurred,priority:3"`
	StrategyID       string    `json:"strategy_id" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:4"`
	OccurredAt       time.Time `json:"occurred_at" gorm:"not null;index:idx_recommendation_events_user_occurred,priority:2;index:idx_recommendation_events_article_occurred,priority:2;index:idx_recommendation_events_dimension_occurred,priority:5"`
	ReceivedAt       time.Time `json:"received_at" gorm:"not null"`
	CreatedAt        time.Time `json:"created_at" gorm:"not null"`
}
