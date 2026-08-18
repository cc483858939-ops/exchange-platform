package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
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
