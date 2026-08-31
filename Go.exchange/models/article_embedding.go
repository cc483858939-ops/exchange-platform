package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

type PostEmbedding struct {
	PostID      uint            `json:"post_id" gorm:"primaryKey;autoIncrement:false"`
	Post        Post            `json:"-" gorm:"foreignKey:PostID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Version     string          `json:"version" gorm:"size:64;not null"`
	Model       string          `json:"model" gorm:"size:128;not null"`
	Dimensions  int             `json:"dimensions" gorm:"not null;check:chk_post_embeddings_dimensions,dimensions > 0"`
	Embedding   pgvector.Vector `json:"-" gorm:"type:vector;not null"`
	ContentHash string          `json:"content_hash" gorm:"size:64;not null"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (PostEmbedding) TableName() string { return "post_embeddings" }
