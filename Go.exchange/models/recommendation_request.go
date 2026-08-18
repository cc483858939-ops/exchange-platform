package models

import "time"

// RecommendationRequest records the serving inputs and aggregate output counts for one
// successful For You response. Result rows are persisted in the same transaction.
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
	SemanticCandidateCount  int       `json:"semantic_candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_semantic_candidates,semantic_candidate_count >= 0"`
	FollowingCandidateCount int       `json:"following_candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_following_candidates,following_candidate_count >= 0"`
	RecentCandidateCount    int       `json:"recent_candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_recent_candidates,recent_candidate_count >= 0"`
	PopularCandidateCount   int       `json:"popular_candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_popular_candidates,popular_candidate_count >= 0"`
	MergedCandidateCount    int       `json:"merged_candidate_count" gorm:"not null;default:0;check:chk_recommendation_request_merged_candidates,merged_candidate_count >= 0"`
	PositiveSignalCount     int       `json:"positive_signal_count" gorm:"not null;default:0;check:chk_recommendation_request_positive_signals,positive_signal_count >= 0"`
	NegativeSignalCount     int       `json:"negative_signal_count" gorm:"not null;default:0;check:chk_recommendation_request_negative_signals,negative_signal_count >= 0"`
	InNetworkResultCount    int       `json:"in_network_result_count" gorm:"not null;default:0;check:chk_recommendation_request_in_network,in_network_result_count >= 0"`
	OutOfNetworkResultCount int       `json:"out_of_network_result_count" gorm:"not null;default:0;check:chk_recommendation_request_out_network, out_of_network_result_count >= 0"`
	NovelAuthorResultCount  int       `json:"novel_author_result_count" gorm:"not null;default:0;check:chk_recommendation_request_novel_author,novel_author_result_count >= 0"`
	SoftServedFallbackCount int       `json:"soft_served_fallback_count" gorm:"not null;default:0;check:chk_recommendation_request_soft_served,soft_served_fallback_count >= 0"`
	PersonalizationMode     string    `json:"personalization_mode" gorm:"size:32;not null;default:'cold_start';check:chk_recommendation_request_personalization_mode,personalization_mode IN ('semantic_social','social_only','cold_start')"`
	FallbackReason          string    `json:"fallback_reason" gorm:"size:64;not null;default:'';check:chk_recommendation_request_fallback,fallback_reason IN ('','no_positive_profile','insufficient_fresh_candidates')"`
	GenerationLatencyMS     int64     `json:"generation_latency_ms" gorm:"not null;default:0;check:chk_recommendation_request_latency,generation_latency_ms >= 0"`
	CreatedAt               time.Time `json:"created_at" gorm:"not null;index:idx_recommendation_requests_user_created,priority:2;index:idx_recommendation_requests_created;index:idx_recommendation_requests_dimension_created,priority:5"`
}
