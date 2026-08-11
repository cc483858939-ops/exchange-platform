package models

import "time"

const (
	ArticleAnalysisJobQueued    = "queued"
	ArticleAnalysisJobLeased    = "leased"
	ArticleAnalysisJobRetryWait = "retry_wait"
	ArticleAnalysisJobSucceeded = "succeeded"
	ArticleAnalysisJobDead      = "dead"
	ArticleAnalysisJobCanceled  = "canceled"
)

// ArticleAnalysisJob is the durable source of truth for AI analysis work.
// Kafka delivers work notifications; a worker always claims this row before
// calling the model so duplicate messages and worker crashes are harmless.
type ArticleAnalysisJob struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	ArticleID        uint       `json:"article_id" gorm:"not null;uniqueIndex"`
	State            string     `json:"state" gorm:"size:32;not null;index:idx_article_analysis_jobs_ready,priority:1"`
	AttemptCount     int        `json:"attempt_count" gorm:"not null;default:0"`
	MaxAttempts      int        `json:"max_attempts" gorm:"not null;default:5"`
	NextAttemptAt    time.Time  `json:"next_attempt_at" gorm:"not null;index:idx_article_analysis_jobs_ready,priority:2"`
	LeaseUntil       *time.Time `json:"lease_until" gorm:"index"`
	LeasedBy         string     `json:"leased_by" gorm:"size:128"`
	LastError        string     `json:"last_error" gorm:"type:text"`
	LastDispatchedAt *time.Time `json:"last_dispatched_at" gorm:"index"`
	FinishedAt       *time.Time `json:"finished_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
