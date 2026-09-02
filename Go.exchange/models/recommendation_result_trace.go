package models

import "time"

// RecommendationResultTrace stores bounded, explainable serving facts without content,
// embeddings, tokens, or raw client payloads.
type RecommendationResultTrace struct {
	RequestID               string    `json:"request_id" gorm:"primaryKey;type:uuid;not null;uniqueIndex:uidx_recommendation_result_trace_request_post,priority:1"`
	Position                int       `json:"position" gorm:"primaryKey;not null;check:chk_recommendation_result_trace_position,position > 0"`
	PostID                  uint      `json:"post_id" gorm:"not null;uniqueIndex:uidx_recommendation_result_trace_request_post,priority:2;index:idx_recommendation_result_trace_post"`
	AuthorID                uint      `json:"author_id" gorm:"not null"`
	FromSemantic            bool      `json:"from_semantic" gorm:"not null;default:false"`
	FromFollowing           bool      `json:"from_following" gorm:"not null;default:false"`
	FromRecent              bool      `json:"from_recent" gorm:"not null;default:false"`
	FromTrending            bool      `json:"from_trending" gorm:"not null;default:false"`
	FusionScore             float64   `json:"fusion_score" gorm:"not null;default:0"`
	SourceCount             int       `json:"source_count" gorm:"not null;default:0"`
	SemanticRank            int       `json:"semantic_rank" gorm:"not null;default:0"`
	FollowingRank           int       `json:"following_rank" gorm:"not null;default:0"`
	RecentRank              int       `json:"recent_rank" gorm:"not null;default:0"`
	TrendingRank            int       `json:"trending_rank" gorm:"not null;default:0"`
	IsInNetwork             bool      `json:"is_in_network" gorm:"not null;default:false"`
	IsNovelAuthor           bool      `json:"is_novel_author" gorm:"not null;default:false"`
	WasSoftServedFallback   bool      `json:"was_soft_served_fallback" gorm:"not null;default:false"`
	PositiveSemantic        float64   `json:"positive_semantic" gorm:"not null;default:0"`
	NegativeSemantic        float64   `json:"negative_semantic" gorm:"not null;default:0"`
	NegativeConfidence      float64   `json:"negative_confidence" gorm:"not null;default:0"`
	InteractionAffinity     float64   `json:"interaction_affinity" gorm:"not null;default:0"`
	FollowingBonusApplied   float64   `json:"following_bonus_applied" gorm:"not null;default:0"`
	SemanticComponent       float64   `json:"semantic_component" gorm:"not null;default:0"`
	TrendingComponent       float64   `json:"trending_component" gorm:"not null;default:0"`
	AuthorAffinityComponent float64   `json:"author_affinity_component" gorm:"not null;default:0"`
	DiversityPenalty        float64   `json:"diversity_penalty" gorm:"not null;default:0"`
	BaseScore               float64   `json:"base_score" gorm:"not null;default:0"`
	FinalScore              float64   `json:"final_score" gorm:"not null;default:0"`
	ExplorationOpportunity  bool      `json:"exploration_opportunity" gorm:"not null;default:false"`
	SelectionMode           string    `json:"selection_mode" gorm:"size:16;not null;default:'ranked'"`
	ExplorationReason       string    `json:"exploration_reason" gorm:"size:32;not null;default:''"`
	ExplorationSemantic     float64   `json:"exploration_semantic" gorm:"not null;default:0"`
	CreatedAt               time.Time `json:"created_at" gorm:"not null;index:idx_recommendation_result_trace_created"`
	ExpiresAt               time.Time `json:"expires_at" gorm:"not null;index:idx_recommendation_result_trace_expires"`
}
