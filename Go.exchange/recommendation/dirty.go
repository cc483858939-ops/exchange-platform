package recommendation

import (
	"errors"
	"sort"
	"strings"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func normalizeDirtyUsers(userIDs []uint) []uint {
	seen := make(map[uint]struct{}, len(userIDs))
	ids := make([]uint, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		ids = append(ids, userID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normalizeDirtyReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 64 {
		return reason[:64]
	}
	return reason
}

// InvalidateProfiles records a true source change. Existing rows advance the
// durable version and reset retry state; absent rows start at version one.
func InvalidateProfiles(tx *gorm.DB, userIDs []uint, reason string, now time.Time) error {
	if tx == nil {
		return errors.New("database is not initialized")
	}
	ids := normalizeDirtyUsers(userIDs)
	if len(ids) == 0 {
		return nil
	}
	reason = normalizeDirtyReason(reason)
	rows := make([]models.UserRecoProfileDirty, 0, len(ids))
	for _, userID := range ids {
		rows = append(rows, models.UserRecoProfileDirty{
			UserID: userID, DirtyVersion: 1, DirtyAt: now, Reason: reason,
			Attempts: 0, NextAttemptAt: now, LastError: "", UpdatedAt: now,
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"dirty_version":   gorm.Expr("user_reco_profile_dirty.dirty_version + 1"),
			"dirty_at":        now,
			"reason":          reason,
			"attempts":        0,
			"next_attempt_at": now,
			"last_error":      "",
			"updated_at":      now,
		}),
	}).Create(&rows).Error
}

// EnsureProfilesQueued creates a queue row for a serving miss/stale result or
// a periodic rebase, without changing an already queued row or its retry data.
func EnsureProfilesQueued(tx *gorm.DB, userIDs []uint, reason string, now time.Time) error {
	if tx == nil {
		return errors.New("database is not initialized")
	}
	ids := normalizeDirtyUsers(userIDs)
	if len(ids) == 0 {
		return nil
	}
	reason = normalizeDirtyReason(reason)
	rows := make([]models.UserRecoProfileDirty, 0, len(ids))
	for _, userID := range ids {
		rows = append(rows, models.UserRecoProfileDirty{
			UserID: userID, DirtyVersion: 1, DirtyAt: now, Reason: reason,
			Attempts: 0, NextAttemptAt: now, LastError: "", UpdatedAt: now,
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(&rows).Error
}
