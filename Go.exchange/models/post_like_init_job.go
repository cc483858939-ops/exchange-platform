package models

import "time"

const (
	PostLikeInitJobPending   = "pending"
	PostLikeInitJobAttempted = "attempted"
)

// PostLikeInitJob is durable work created in the same transaction as a new
// Post. Attempted rows may only be cleared after validating complete Redis
// state; they must never bootstrap missing state again.
type PostLikeInitJob struct {
	PostID     uint      `gorm:"primaryKey;autoIncrement:false;check:chk_post_like_init_jobs_post_positive,post_id > 0;index:idx_post_like_init_jobs_due,priority:2"`
	RetryAfter time.Time `gorm:"not null;index:idx_post_like_init_jobs_due,priority:1"`
	Attempts   int       `gorm:"not null;default:0;check:chk_post_like_init_jobs_attempts_nonnegative,attempts >= 0"`
	State      string    `gorm:"size:16;not null;default:pending;check:chk_post_like_init_jobs_state,state IN ('pending','attempted')"`
	CreatedAt  time.Time
}
