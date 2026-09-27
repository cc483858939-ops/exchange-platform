package models

import "time"

// PostMediaUpload is the durable lease for an uploaded user Post image until
// create-post consumes it into post_media.
type PostMediaUpload struct {
	MediaID string `json:"-" gorm:"type:uuid;primaryKey"`
	OwnerID uint   `json:"-" gorm:"not null"`
	Status  string `json:"-" gorm:"size:16;not null;index:idx_post_media_uploads_status_cleanup_after,priority:1"`

	OriginalObjectKey string `json:"-" gorm:"size:512;not null"`
	MediumObjectKey   string `json:"-" gorm:"size:512;not null"`
	LargeObjectKey    string `json:"-" gorm:"size:512;not null"`
	ManifestObjectKey string `json:"-" gorm:"size:512;not null"`

	MediumURL string `json:"-" gorm:"size:512;not null"`
	LargeURL  string `json:"-" gorm:"size:512;not null"`
	Width     int    `json:"-" gorm:"not null"`
	Height    int    `json:"-" gorm:"not null"`

	CreatedAt    time.Time  `json:"-" gorm:"not null"`
	UploadedAt   *time.Time `json:"-"`
	CleanupAfter time.Time  `json:"-" gorm:"not null;index:idx_post_media_uploads_status_cleanup_after,priority:2"`
}

func (PostMediaUpload) TableName() string { return "post_media_uploads" }
