package models

import "time"

// RecommendationDailyMetric is a rebuildable projection over
// direct Kafka recommendation behavior events.
type RecommendationDailyMetric struct {
	MetricDate             time.Time `json:"metric_date" gorm:"type:date;primaryKey"`
	Scene                  string    `json:"scene" gorm:"size:64;primaryKey"`
	RankerVersion          string    `json:"ranker_version" gorm:"size:64;primaryKey"`
	RankerConfigHash       string    `json:"ranker_config_hash" gorm:"size:32;primaryKey"`
	StrategyID             string    `json:"strategy_id" gorm:"size:64;primaryKey"`
	ExplorationOpportunity bool      `json:"exploration_opportunity" gorm:"primaryKey;not null;default:false"`
	SelectionMode          string    `json:"selection_mode" gorm:"size:16;primaryKey;not null;default:'ranked'"`
	ExplorationReason      string    `json:"exploration_reason" gorm:"size:32;primaryKey;not null;default:''"`
	Position               int       `json:"position" gorm:"primaryKey;check:chk_recommendation_metric_position,position > 0"`
	ArticleID              uint      `json:"article_id" gorm:"primaryKey"`
	ImpressionCount        int64     `json:"impression_count" gorm:"not null;default:0;check:chk_recommendation_metric_impressions,impression_count >= 0"`
	ClickCount             int64     `json:"click_count" gorm:"not null;default:0;check:chk_recommendation_metric_clicks,click_count >= 0"`
	QualifiedReadCount     int64     `json:"qualified_read_count" gorm:"not null;default:0;check:chk_recommendation_metric_qualified_reads,qualified_read_count >= 0"`
	QuickBounceCount       int64     `json:"quick_bounce_count" gorm:"not null;default:0;check:chk_recommendation_metric_quick_bounces,quick_bounce_count >= 0"`
	NotInterestedCount     int64     `json:"not_interested_count" gorm:"not null;default:0;check:chk_recommendation_metric_not_interested,not_interested_count >= 0"`
	FeedDwellCount         int64     `json:"feed_dwell_count" gorm:"not null;default:0;check:chk_recommendation_metric_feed_dwell_count,feed_dwell_count >= 0"`
	FeedVisibleTimeMS      int64     `json:"feed_visible_time_ms" gorm:"not null;default:0;check:chk_recommendation_metric_feed_visible_time,feed_visible_time_ms >= 0"`
	UpdatedAt              time.Time `json:"updated_at" gorm:"not null"`
}
