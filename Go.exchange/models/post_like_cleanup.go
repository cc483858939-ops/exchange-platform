package models

import "time"

// PostLikeCleanup is committed with Post deletion. Rows remain until Redis has
// fenced the deleted identity and removed its live Like state.
type PostLikeCleanup struct {
	PostID     uint      `gorm:"primaryKey;autoIncrement:false;check:chk_post_like_cleanup_post_positive,post_id > 0;index:idx_post_like_cleanup_due,priority:2"`
	RetryAfter time.Time `gorm:"not null;index:idx_post_like_cleanup_due,priority:1"`
}
