package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

const (
	ArticleEmbeddingJobQueued    = "queued"
	ArticleEmbeddingJobLeased    = "leased"
	ArticleEmbeddingJobRetryWait = "retry_wait"
	ArticleEmbeddingJobSucceeded = "succeeded"
	ArticleEmbeddingJobDead      = "dead"
)

type ArticleEmbedding struct {
	ArticleID   uint            `json:"article_id" gorm:"primaryKey;autoIncrement:false"`
	Article     Article         `json:"-" gorm:"foreignKey:ArticleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Version     string          `json:"version" gorm:"size:64;not null"`
	Model       string          `json:"model" gorm:"size:128;not null"`
	Dimensions  int             `json:"dimensions" gorm:"not null;check:chk_article_embeddings_dimensions,dimensions > 0"`
	Embedding   pgvector.Vector `json:"-" gorm:"type:vector;not null"`
	ContentHash string          `json:"content_hash" gorm:"size:64;not null"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type ArticleEmbeddingJob struct {
	ID            uint       `json:"id" gorm:"primaryKey"`
	ArticleID     uint       `json:"article_id" gorm:"not null;uniqueIndex"`
	Article       Article    `json:"-" gorm:"foreignKey:ArticleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	State         string     `json:"state" gorm:"size:32;not null;index:idx_article_embedding_jobs_ready,priority:1"`
	AttemptCount  int        `json:"attempt_count" gorm:"not null;default:0"`
	MaxAttempts   int        `json:"max_attempts" gorm:"not null;default:5;check:chk_article_embedding_jobs_max_attempts,max_attempts > 0"`
	NextAttemptAt time.Time  `json:"next_attempt_at" gorm:"not null;index:idx_article_embedding_jobs_ready,priority:2"`
	LeaseUntil    *time.Time `json:"lease_until" gorm:"index"`
	LeasedBy      string     `json:"leased_by" gorm:"size:128"`
	LastError     string     `json:"last_error" gorm:"type:text"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}
