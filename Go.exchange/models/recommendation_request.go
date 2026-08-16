package models

import "time"

// RecommendationRequest records a successful recommendation response. It is
// deliberately separate from the recommendation behavior projection: a response is a serving
// opportunity, whereas events are client-observed facts.
type RecommendationRequest struct {
	RequestID               string    `json:"request_id" gorm:"primaryKey;type:uuid"`
	UserID                  uint      `json:"user_id" gorm:"not null;index:idx_recommendation_requests_user_created,priority:1"`
	Scene                   string    `json:"scene" gorm:"size:64;not null;index:idx_recommendation_requests_dimension_created,priority:1"`
	StrategyID              string    `json:"strategy_id" gorm:"size:64;not null;index:idx_recommendation_requests_dimension_created,priority:2"`
	RankerVersion           string    `json:"ranker_version" gorm:"size:64;not null;index:idx_recommendation_requests_dimension_created,priority:3"`
	RankerConfigHash        string    `json:"ranker_config_hash" gorm:"size:32;not null;index:idx_recommendation_requests_dimension_created,priority:4"`
	RequestedLimit          int       `json:"requested_limit" gorm:"not null;check:chk_recommendation_request_limit,requested_limit > 0"`
	CandidateCount          int       `json:"candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_candidates,candidate_count >= 0"`
	ResultCount             int       `json:"result_count" gorm:"not null;default:0;check:chk_recommendation_request_results,result_count >= 0"`
	TrackedResultCount      int       `json:"tracked_result_count" gorm:"not null;default:0;check:chk_recommendation_request_tracked,tracked_result_count >= 0 AND tracked_result_count <= result_count"`
	PersonalizedSignalCount int       `json:"personalized_signal_count" gorm:"not null;default:0;check:chk_recommendation_request_signals,personalized_signal_count >= 0"`
	FallbackReason          string    `json:"fallback_reason" gorm:"size:64;not null;default:'';check:chk_recommendation_request_fallback,fallback_reason IN ('','no_user_behavior','insufficient_candidates')"`
	GenerationLatencyMS     int64     `json:"generation_latency_ms" gorm:"not null;default:0;check:chk_recommendation_request_latency,generation_latency_ms >= 0"`
	CreatedAt               time.Time `json:"created_at" gorm:"not null;index:idx_recommendation_requests_user_created,priority:2;index:idx_recommendation_requests_created;index:idx_recommendation_requests_dimension_created,priority:5"`
}
