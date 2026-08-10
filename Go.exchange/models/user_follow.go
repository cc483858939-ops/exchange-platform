package models

import "time"

type UserFollow struct {
	ID          uint      `gorm:"primaryKey;index:idx_user_follows_follower_created,priority:3,sort:desc;index:idx_user_follows_following_created,priority:3,sort:desc"`
	FollowerID  uint      `gorm:"not null;uniqueIndex:uidx_user_follows_pair,priority:1;index:idx_user_follows_follower_created,priority:1"`
	FollowingID uint      `gorm:"not null;uniqueIndex:uidx_user_follows_pair,priority:2;index:idx_user_follows_following_created,priority:1"`
	CreatedAt   time.Time `gorm:"not null;autoCreateTime;index:idx_user_follows_follower_created,priority:2,sort:desc;index:idx_user_follows_following_created,priority:2,sort:desc"`

	Follower  User `gorm:"foreignKey:FollowerID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Following User `gorm:"foreignKey:FollowingID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}
