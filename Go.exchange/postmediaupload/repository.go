// Package postmediaupload owns the short-lived database lease for user Post
// media between object upload and transactional Post binding.
package postmediaupload

import (
	"context"
	"errors"
	"fmt"
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	StatusUploading = "uploading"
	StatusUploaded  = "uploaded"

	DefaultUploadTimeout = 30 * time.Minute
	DefaultGracePeriod   = 24 * time.Hour
)

var ErrUploadUnavailable = errors.New("post media upload is unavailable")

func CreateUploading(ctx context.Context, db *gorm.DB, upload models.PostMediaUpload) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if ctx == nil {
		return errors.New("upload context is nil")
	}
	if upload.MediaID == "" || upload.OwnerID == 0 || upload.Status != StatusUploading || upload.CleanupAfter.IsZero() {
		return errors.New("invalid uploading post media record")
	}
	return db.WithContext(ctx).Create(&upload).Error
}

// MarkUploaded performs the uploading -> uploaded compare-and-swap.
func MarkUploaded(ctx context.Context, db *gorm.DB, mediaID string, ownerID uint, uploadedAt, cleanupAfter time.Time) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if ctx == nil {
		return errors.New("upload context is nil")
	}
	if mediaID == "" || ownerID == 0 || uploadedAt.IsZero() || cleanupAfter.Before(uploadedAt) {
		return errors.New("invalid uploaded post media transition")
	}
	result := db.WithContext(ctx).Model(&models.PostMediaUpload{}).
		Where("media_id = ? AND owner_id = ? AND status = ?", mediaID, ownerID, StatusUploading).
		Updates(map[string]interface{}{
			"status":        StatusUploaded,
			"uploaded_at":   uploadedAt,
			"cleanup_after": cleanupAfter,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrUploadUnavailable
	}
	return nil
}

// DeleteAfterObjectCleanup removes an uploading lease only after the caller
// has confirmed that all expected storage objects were removed. Uploaded
// leases must retain their complete payload after finalization errors.
func DeleteAfterObjectCleanup(ctx context.Context, db *gorm.DB, mediaID string, ownerID uint) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if ctx == nil {
		return errors.New("upload context is nil")
	}
	result := db.WithContext(ctx).Where("media_id = ? AND owner_id = ? AND status = ?", mediaID, ownerID, StatusUploading).
		Delete(&models.PostMediaUpload{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrUploadUnavailable
	}
	return nil
}

// LockForConsumption loads the complete requested set in a deterministic lock
// order. The caller validates owner, status, and URL while these locks are held.
func LockForConsumption(tx *gorm.DB, mediaIDs []string) ([]models.PostMediaUpload, error) {
	if tx == nil {
		return nil, errors.New("database transaction is not initialized")
	}
	if len(mediaIDs) == 0 {
		return []models.PostMediaUpload{}, nil
	}
	var uploads []models.PostMediaUpload
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("media_id IN ?", mediaIDs).
		Order("media_id ASC").
		Find(&uploads).Error
	if err != nil {
		return nil, fmt.Errorf("lock pending Post media uploads: %w", err)
	}
	return uploads, nil
}

// DeleteConsumed removes exactly the uploaded, owner-matched set previously
// locked by LockForConsumption in the same transaction as PostMedia creation.
func DeleteConsumed(tx *gorm.DB, mediaIDs []string, ownerID uint) error {
	if tx == nil {
		return errors.New("database transaction is not initialized")
	}
	if len(mediaIDs) == 0 {
		return nil
	}
	result := tx.Where("media_id IN ? AND owner_id = ? AND status = ?", mediaIDs, ownerID, StatusUploaded).
		Delete(&models.PostMediaUpload{})
	if result.Error != nil {
		return fmt.Errorf("consume pending Post media uploads: %w", result.Error)
	}
	if result.RowsAffected != int64(len(mediaIDs)) {
		return ErrUploadUnavailable
	}
	return nil
}
