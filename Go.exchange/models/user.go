package models

import (
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Username      string `gorm:"unique"`
	Password      string `json:"-"`
	AuthVersion   int64  `gorm:"not null;default:1"`
	DisplayName   string `gorm:"type:varchar(50);not null;default:''"`
	Bio           string `gorm:"type:varchar(160);not null;default:''"`
	AvatarURL     string `gorm:"type:varchar(512);not null;default:''"`
	CoverImageURL string `gorm:"type:varchar(512);not null;default:''"`
}
