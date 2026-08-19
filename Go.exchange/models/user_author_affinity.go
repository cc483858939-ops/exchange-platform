package models

import "time"

// UserAuthorAffinity stores unsaturated, rebuildable affinity. Saturation is
// intentionally applied at rank time using the active serving configuration.
type UserAuthorAffinity struct {
	UserID      uint      `json:"user_id" gorm:"primaryKey;autoIncrement:false;index:idx_user_author_affinities_author_user,priority:2"`
	AuthorID    uint      `json:"author_id" gorm:"primaryKey;autoIncrement:false;index:idx_user_author_affinities_author_user,priority:1"`
	RawAffinity float64   `json:"raw_affinity" gorm:"not null;default:0"`
	RebuiltAt   time.Time `json:"rebuilt_at" gorm:"not null"`
}

func (UserAuthorAffinity) TableName() string { return "user_author_affinities" }
