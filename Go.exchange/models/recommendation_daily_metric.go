package models

import "time"

// RecommendationDailyMetric is a rebuildable projection over
// RecommendationEvent facts.
type RecommendationDailyMetric struct {
	MetricDate       time.Time `json:"metric_date" gorm:"type:date;primaryKey"`
	Scene            string    `json:"scene" gorm:"size:64;primaryKey"`
	RankerVersion    string    `json:"ranker_version" gorm:"size:64;primaryKey"`
	RankerConfigHash string    `json:"ranker_config_hash" gorm:"size:32;primaryKey"`
	StrategyID       string    `json:"strategy_id" gorm:"size:64;primaryKey"`
	Position         int       `json:"position" gorm:"primaryKey;check:chk_recommendation_metric_position,position > 0"`
	ArticleID        uint      `json:"article_id" gorm:"primaryKey"`
	ImpressionCount  int64     `json:"impression_count" gorm:"not null;default:0;check:chk_recommendation_metric_impressions,impression_count >= 0"`
	ClickCount       int64     `json:"click_count" gorm:"not null;default:0;check:chk_recommendation_metric_clicks,click_count >= 0"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"not null"`
}
