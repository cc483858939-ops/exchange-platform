package models

import "time"

// DevDataMirrorAccount stores the durable identity binding between a
// repository-controlled source registry entry and its local mirror user.
// It intentionally lives outside the runtime API schema contract.
type DevDataMirrorAccount struct {
	ID                uint       `gorm:"primaryKey"`
	RegistryKey       string     `gorm:"type:varchar(64);not null;uniqueIndex:ucon_devdata_mirror_accounts_registry_key"`
	Platform          string     `gorm:"type:varchar(16);not null;uniqueIndex:ucon_devdata_mirror_accounts_platform_source_user,priority:1"`
	SourceUserID      string     `gorm:"type:varchar(64);not null;uniqueIndex:ucon_devdata_mirror_accounts_platform_source_user,priority:2"`
	SourceHandle      string     `gorm:"type:varchar(64);not null"`
	LocalUserID       uint       `gorm:"not null;uniqueIndex:ucon_devdata_mirror_accounts_local_user"`
	Category          string     `gorm:"type:varchar(64);not null"`
	Enabled           bool       `gorm:"not null;default:true"`
	SourceAvatarURL   string     `gorm:"type:varchar(512);not null;default:''"`
	AvatarObjectKey   string     `gorm:"type:varchar(512);not null;default:''"`
	AvatarContentHash string     `gorm:"type:varchar(64);not null;default:''"`
	LastFetchedAt     *time.Time `gorm:"index"`
	CreatedAt         time.Time  `gorm:"not null"`
	UpdatedAt         time.Time  `gorm:"not null"`
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
	Platform          string    `gorm:"type:varchar(16);not null;uniqueIndex:ucon_devdata_mirror_posts_platform_source_post,priority:1"`
	SourcePostID      string    `gorm:"type:varchar(64);not null;uniqueIndex:ucon_devdata_mirror_posts_platform_source_post,priority:2"`
	SourceURL         string    `gorm:"type:varchar(512);not null"`
	MirrorAccountID   uint      `gorm:"not null"`
	LocalPostID       uint      `gorm:"not null;uniqueIndex:ucon_devdata_mirror_posts_local_post"`
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
