package models

import "time"

const (
	RecommendationEventTypeImpression    = "impression"
	RecommendationEventTypeClick         = "click"
	RecommendationEventTypeReadEnd       = "read_end"
	RecommendationEventTypeNotInterested = "not_interested"
)

// RecommendationEvent is the durable, server-attributed fact used to rebuild
// recommendation telemetry projections.
type RecommendationEvent struct {
	EventID               string    `json:"event_id" gorm:"primaryKey;type:uuid"`
	UserID                uint      `json:"user_id" gorm:"not null;index:idx_recommendation_events_user_occurred,priority:1"`
	RequestID             string    `json:"request_id" gorm:"type:uuid;not null;uniqueIndex:idx_recommendation_events_business,priority:1"`
	ArticleID             uint      `json:"article_id" gorm:"not null;uniqueIndex:idx_recommendation_events_business,priority:2;index:idx_recommendation_events_article_occurred,priority:1"`
	EventType             string    `json:"event_type" gorm:"size:16;not null;uniqueIndex:idx_recommendation_events_business,priority:3;check:chk_recommendation_event_type,event_type IN ('impression','click','read_end','not_interested')"`
	Scene                 string    `json:"scene" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:1"`
	Position              int       `json:"position" gorm:"not null;check:chk_recommendation_event_position,position > 0"`
	RankerVersion         string    `json:"ranker_version" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:2"`
	RankerConfigHash      string    `json:"ranker_config_hash" gorm:"size:32;not null;index:idx_recommendation_events_dimension_occurred,priority:3"`
	StrategyID            string    `json:"strategy_id" gorm:"size:64;not null;index:idx_recommendation_events_dimension_occurred,priority:4"`
	OccurredAt            time.Time `json:"occurred_at" gorm:"not null;index:idx_recommendation_events_user_occurred,priority:2;index:idx_recommendation_events_article_occurred,priority:2;index:idx_recommendation_events_dimension_occurred,priority:5"`
	ReceivedAt            time.Time `json:"received_at" gorm:"not null"`
	ForegroundTimeMS      *int64    `json:"foreground_time_ms,omitempty" gorm:"check:chk_recommendation_event_foreground_time,foreground_time_ms IS NULL OR foreground_time_ms BETWEEN 0 AND 21600000"`
	ScrollProgressPercent *int      `json:"scroll_progress_percent,omitempty" gorm:"column:scroll_progress_percent"`
	ExitType              *string   `json:"exit_type,omitempty" gorm:"size:32"`
	EstimatedReadTimeMS   *int64    `json:"estimated_read_time_ms,omitempty" gorm:"column:estimated_read_time_ms"`
	ReadPolicyVersion     *string   `json:"read_policy_version,omitempty" gorm:"column:read_policy_version,size:32"`
	ReadOutcome           *string   `json:"read_outcome,omitempty" gorm:"column:read_outcome,size:16"`
	CreatedAt             time.Time `json:"created_at" gorm:"not null"`
}
