package models

import "gorm.io/gorm"

type Comment struct {
	gorm.Model

	ArticleID uint   `json:"-" gorm:"not null;index"`
	UserID    uint   `json:"-" gorm:"not null;index"`
	Content   string `json:"content" gorm:"type:text;not null"`

	Article Article `json:"-" gorm:"foreignKey:ArticleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Author  User    `json:"-" gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}
