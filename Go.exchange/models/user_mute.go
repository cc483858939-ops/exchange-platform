package models

import "time"

// UserMute stores a viewer's persistent preference to hide another account's
// authored content and activity from personalized surfaces.
type UserMute struct {
	ID        uint      `gorm:"primaryKey"`
	MuterID   uint      `gorm:"not null;uniqueIndex:uidx_user_mutes_pair,priority:1"`
	MutedID   uint      `gorm:"not null;uniqueIndex:uidx_user_mutes_pair,priority:2;index:idx_user_mutes_muted_id"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`

	Muter User `gorm:"foreignKey:MuterID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Muted User `gorm:"foreignKey:MutedID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}
