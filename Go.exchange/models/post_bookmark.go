package models

import "time"

// PostBookmark records one user's private bookmark relation to a canonical
// Post. Removing a bookmark hard-deletes this row; the relation is not an
// engagement counter or an event stream.
type PostBookmark struct {
	UserID    uint      `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	PostID    uint      `json:"post_id" gorm:"primaryKey;autoIncrement:false"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;autoCreateTime"`
	User      User      `json:"-" gorm:"foreignKey:UserID;references:ID"`
	Post      Post      `json:"-" gorm:"foreignKey:PostID;references:ID"`
}

func (PostBookmark) TableName() string { return "post_bookmarks" }
