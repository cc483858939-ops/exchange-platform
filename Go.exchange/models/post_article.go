package models

import "time"

// PostArticle contains the optional published long-form metadata attached to
// a root Post. The body and engagement counters remain on Post.
type PostArticle struct {
	PostID           uint       `json:"post_id" gorm:"primaryKey;autoIncrement:false"`
	Title            string     `json:"title" gorm:"type:text;not null"`
	Preview          string     `json:"preview" gorm:"type:text;not null"`
	CoverImageURL    string     `json:"cover_image_url" gorm:"size:512;not null"`
	PublicationState string     `json:"publication_state" gorm:"size:32;not null;default:published"`
	PublishedAt      *time.Time `json:"published_at" gorm:"not null"`
	ExpiredAt        *time.Time `json:"expired_at"`
	Post             Post       `json:"-" gorm:"foreignKey:PostID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

func (PostArticle) TableName() string { return "post_articles" }
