package models

import "time"

// UserArticleRecoState is the rebuildable, canonical interaction projection
// used by recommendation serving for fast interaction exclusion.
type UserArticleRecoState struct {
	UserID           uint       `json:"user_id" gorm:"primaryKey;autoIncrement:false;index:idx_user_article_reco_states_article_user,priority:2"`
	ArticleID        uint       `json:"article_id" gorm:"primaryKey;autoIncrement:false;index:idx_user_article_reco_states_article_user,priority:1"`
	Interacted       bool       `json:"interacted" gorm:"not null;default:true"`
	LikeAt           *time.Time `json:"like_at"`
	ReplyAt          *time.Time `json:"reply_at"`
	PassiveSignal    string     `json:"passive_signal" gorm:"size:32;not null;default:''"`
	PassiveSignalAt  *time.Time `json:"passive_signal_at"`
	NegativeSignal   string     `json:"negative_signal" gorm:"size:32;not null;default:''"`
	NegativeSignalAt *time.Time `json:"negative_signal_at"`
	CanonicalVersion string     `json:"canonical_version" gorm:"size:64;not null"`
	RebuiltAt        time.Time  `json:"rebuilt_at" gorm:"not null;index"`
}

func (UserArticleRecoState) TableName() string { return "user_article_reco_states" }
