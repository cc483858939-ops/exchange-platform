package recommendation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DirtyProfile struct {
	UserID        uint
	DirtyVersion  int64
	DirtyAt       time.Time
	Reason        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
}

type GormDirtyProfileRepository struct {
	db *gorm.DB
}

func NewGormDirtyProfileRepository(db *gorm.DB) (*GormDirtyProfileRepository, error) {
	if db == nil {
		return nil, errors.New("recommendation dirty-profile repository database is nil")
	}
	return &GormDirtyProfileRepository{db: db}, nil
}

func (r *GormDirtyProfileRepository) InvalidateProfiles(ctx context.Context, userIDs []uint, reason string, now time.Time) error {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return fmt.Errorf("invalidate recommendation profiles: %w", err)
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
	return db.Clauses(clause.OnConflict{
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

func (r *GormDirtyProfileRepository) EnsureProfilesQueued(ctx context.Context, userIDs []uint, reason string, now time.Time) error {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return fmt.Errorf("queue recommendation profiles: %w", err)
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
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(&rows).Error
}

func (r *GormDirtyProfileRepository) ListDue(ctx context.Context, cutoff, now time.Time, limit int) ([]DirtyProfile, error) {
	if limit <= 0 {
		return []DirtyProfile{}, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("list due recommendation profiles: %w", err)
	}
	var rows []models.UserRecoProfileDirty
	if err := db.Where("dirty_at <= ? AND next_attempt_at <= ?", cutoff, now).
		Order("dirty_at ASC, user_id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list due recommendation profiles: %w", err)
	}
	result := make([]DirtyProfile, 0, len(rows))
	for _, row := range rows {
		result = append(result, dirtyProfileFromModel(row))
	}
	return result, nil
}

func (r *GormDirtyProfileRepository) Load(ctx context.Context, userID uint) (DirtyProfile, error) {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return DirtyProfile{}, fmt.Errorf("load recommendation profile claim: %w", err)
	}
	var row models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DirtyProfile{}, err
		}
		return DirtyProfile{}, fmt.Errorf("load recommendation profile claim: %w", err)
	}
	return dirtyProfileFromModel(row), nil
}

func (r *GormDirtyProfileRepository) DeleteClaim(ctx context.Context, userID uint, dirtyVersion int64) (int64, error) {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return 0, fmt.Errorf("delete recommendation profile claim: %w", err)
	}
	result := db.Where("user_id = ? AND dirty_version = ?", userID, dirtyVersion).Delete(&models.UserRecoProfileDirty{})
	return result.RowsAffected, result.Error
}

func (r *GormDirtyProfileRepository) RetryClaim(ctx context.Context, claim DirtyProfile, materializationErr error, now time.Time) error {
	if materializationErr == nil {
		return errors.New("recommendation profile retry requires a cause")
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return fmt.Errorf("retry recommendation profile claim: %w", err)
	}
	attempt := claim.Attempts + 1
	backoffSeconds := 2.0 * math.Pow(2, float64(attempt-1))
	if backoffSeconds > 300 {
		backoffSeconds = 300
	}
	lastError := strings.TrimSpace(materializationErr.Error())
	if len(lastError) > 512 {
		lastError = lastError[:512]
	}
	return db.Model(&models.UserRecoProfileDirty{}).
		Where("user_id = ? AND dirty_version = ?", claim.UserID, claim.DirtyVersion).
		Updates(map[string]interface{}{
			"attempts": attempt, "next_attempt_at": now.Add(time.Duration(backoffSeconds * float64(time.Second))),
			"last_error": lastError, "updated_at": now,
		}).Error
}

func dirtyProfileFromModel(row models.UserRecoProfileDirty) DirtyProfile {
	return DirtyProfile{
		UserID: row.UserID, DirtyVersion: row.DirtyVersion, DirtyAt: row.DirtyAt, Reason: row.Reason,
		Attempts: row.Attempts, NextAttemptAt: row.NextAttemptAt, LastError: row.LastError,
	}
}

var _ DirtyProfileRepository = (*GormDirtyProfileRepository)(nil)
