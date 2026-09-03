package models

import "time"

// PostMedia stores one ordered image attachment for a Post. Media is kept as
// an explicit child model so Post loaders and recommendation code only opt in
// to the additional query when they are building a public response.
type PostMedia struct {
	ID        uint      `json:"-" gorm:"primaryKey;autoIncrement"`
	PostID    uint      `json:"-" gorm:"not null"`
	MediaType string    `json:"type" gorm:"not null"`
	URL       string    `json:"url" gorm:"not null"`
	Position  int       `json:"position" gorm:"not null"`
	CreatedAt time.Time `json:"-" gorm:"not null"`
}

func (PostMedia) TableName() string { return "post_media" }
