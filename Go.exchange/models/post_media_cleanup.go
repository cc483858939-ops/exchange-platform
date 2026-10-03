package models

import "time"

// PostMediaCleanup is durable deletion work for a consumed user upload.
// It is created in the same transaction as the Post's soft deletion.
type PostMediaCleanup struct {
	MediaID         string    `gorm:"type:uuid;primaryKey;index:idx_post_media_cleanup_due,priority:2"`
	OwnerID         uint      `gorm:"not null"`
	MediumObjectKey string    `gorm:"size:512;not null"`
	LargeObjectKey  string    `gorm:"size:512;not null"`
	CreatedAt       time.Time `gorm:"not null"`
	CleanupAfter    time.Time `gorm:"not null;index:idx_post_media_cleanup_due,priority:1"`
	ClaimToken      *string   `gorm:"type:uuid"`
	Attempts        int64     `gorm:"not null;default:0"`
	LastError       *string   `gorm:"type:text"`
}

func (PostMediaCleanup) TableName() string { return "post_media_cleanup" }
