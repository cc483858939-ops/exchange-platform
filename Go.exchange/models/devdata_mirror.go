package models

import "time"

// DevDataMirrorAccount stores the durable identity binding between a
// repository-controlled source registry entry and its local mirror user.
// It intentionally lives outside the runtime API schema contract.
type DevDataMirrorAccount struct {
	ID            uint       `gorm:"primaryKey"`
	RegistryKey   string     `gorm:"type:varchar(64);not null"`
	Platform      string     `gorm:"type:varchar(16);not null"`
	SourceUserID  string     `gorm:"type:varchar(64);not null"`
	SourceHandle  string     `gorm:"type:varchar(64);not null"`
	LocalUserID   uint       `gorm:"not null"`
	Category      string     `gorm:"type:varchar(64);not null"`
	Enabled       bool       `gorm:"not null;default:true"`
	LastFetchedAt *time.Time `gorm:"index"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

func (DevDataMirrorAccount) TableName() string { return "devdata_mirror_accounts" }

const (
	DevDataMirrorPostStateActive    = "active"
	DevDataMirrorPostStateTombstone = "tombstone"
)

// DevDataMirrorPost maps an immutable source Post identity to the canonical
// local Post row. Source metrics are metadata only and never overwrite the
// canonical NexusFeed engagement counters.
type DevDataMirrorPost struct {
	ID                uint      `gorm:"primaryKey"`
	Platform          string    `gorm:"type:varchar(16);not null"`
	SourcePostID      string    `gorm:"type:varchar(64);not null"`
	SourceURL         string    `gorm:"type:varchar(512);not null"`
	MirrorAccountID   uint      `gorm:"not null"`
	LocalPostID       uint      `gorm:"not null"`
	SourceCreatedAt   time.Time `gorm:"not null"`
	SourceLikeCount   int64     `gorm:"not null;default:0"`
	SourceReplyCount  int64     `gorm:"not null;default:0"`
	SourceRepostCount int64     `gorm:"not null;default:0"`
	SourceQuoteCount  int64     `gorm:"not null;default:0"`
	ContentHash       string    `gorm:"type:varchar(64);not null"`
	State             string    `gorm:"type:varchar(16);not null"`
	ImportedAt        time.Time `gorm:"not null"`
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (DevDataMirrorPost) TableName() string { return "devdata_mirror_posts" }
