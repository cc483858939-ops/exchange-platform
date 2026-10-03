package postmediaupload

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"Go.exchange/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	StatusCleanupPending             = "cleanup_pending"
	MaxCleanupClaimBatchSize         = 1000
	MaxCleanupErrorBytes             = 1024
	CleanupErrorStorageDeleteFailure = "storage_delete_failed"
)

var ErrStaleCleanupClaim = errors.New("post media cleanup claim is stale")

type CleanupClaim struct {
	Published         bool
	MediaID           string
	OwnerID           uint
	OriginalObjectKey string
	MediumObjectKey   string
	LargeObjectKey    string
	ManifestObjectKey string
	ClaimToken        string
	CleanupAttempts   int64
}

// ClaimCleanupBatch atomically claims expired uploads while holding their row
// locks. Storage work must happen only after this transaction commits.
func ClaimCleanupBatch(ctx context.Context, db *gorm.DB, now time.Time, claimTimeout time.Duration, batchSize int) ([]CleanupClaim, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("cleanup context is nil")
	}
	if now.IsZero() || claimTimeout <= 0 {
		return nil, errors.New("cleanup claim time and timeout must be positive")
	}
	if batchSize < 1 || batchSize > MaxCleanupClaimBatchSize {
		return nil, fmt.Errorf("cleanup batch size must be between 1 and %d", MaxCleanupClaimBatchSize)
	}

	now = now.UTC()
	claimUntil := now.Add(claimTimeout)
	claims := make([]CleanupClaim, 0, batchSize)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var uploads []models.PostMediaUpload
		if err := cleanupCandidatesQuery(tx, now, batchSize).Find(&uploads).Error; err != nil {
			return fmt.Errorf("select expired Post media cleanup candidates: %w", err)
		}

		for _, upload := range uploads {
			token := uuid.NewString()
			result := tx.Model(&models.PostMediaUpload{}).
				Where("media_id = ? AND status IN ? AND cleanup_after <= ?", upload.MediaID, []string{StatusUploading, StatusUploaded, StatusCleanupPending}, now).
				Updates(map[string]interface{}{
					"status":              StatusCleanupPending,
					"cleanup_claim_token": token,
					"cleanup_claimed_at":  now,
					"cleanup_after":       claimUntil,
					"cleanup_attempts":    gorm.Expr("cleanup_attempts + 1"),
				})
			if result.Error != nil {
				return fmt.Errorf("claim Post media cleanup row %s: %w", upload.MediaID, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("claim Post media cleanup row %s: %w", upload.MediaID, ErrStaleCleanupClaim)
			}
			claims = append(claims, CleanupClaim{
				MediaID:           upload.MediaID,
				OwnerID:           upload.OwnerID,
				OriginalObjectKey: upload.OriginalObjectKey,
				MediumObjectKey:   upload.MediumObjectKey,
				LargeObjectKey:    upload.LargeObjectKey,
				ManifestObjectKey: upload.ManifestObjectKey,
				ClaimToken:        token,
				CleanupAttempts:   upload.CleanupAttempts + 1,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func cleanupCandidatesQuery(tx *gorm.DB, now time.Time, batchSize int) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("status IN ? AND cleanup_after <= ?", []string{StatusUploading, StatusUploaded, StatusCleanupPending}, now).
		Order("cleanup_after ASC, media_id ASC").
		Limit(batchSize)
}

// CompleteCleanup removes a lease only when the caller still owns its claim.
func CompleteCleanup(ctx context.Context, db *gorm.DB, claim CleanupClaim) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if ctx == nil {
		return errors.New("cleanup context is nil")
	}
	if claim.MediaID == "" || claim.ClaimToken == "" {
		return errors.New("cleanup claim identity is incomplete")
	}
	result := db.WithContext(ctx).Where("media_id = ? AND status = ? AND cleanup_claim_token = ?", claim.MediaID, StatusCleanupPending, claim.ClaimToken).
		Delete(&models.PostMediaUpload{})
	if result.Error != nil {
		return fmt.Errorf("delete completed Post media cleanup row: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrStaleCleanupClaim
	}
	return nil
}

// ScheduleCleanupRetry releases a failed claim and records its next eligible
// time. A stale claimant cannot modify a newer claim's retry state.
func ScheduleCleanupRetry(ctx context.Context, db *gorm.DB, claim CleanupClaim, retryAt time.Time, cleanupError string) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if ctx == nil {
		return errors.New("cleanup context is nil")
	}
	if claim.MediaID == "" || claim.ClaimToken == "" || retryAt.IsZero() {
		return errors.New("cleanup retry identity and time are required")
	}
	errorSummary := boundedCleanupError(cleanupError)
	result := db.WithContext(ctx).Model(&models.PostMediaUpload{}).
		Where("media_id = ? AND status = ? AND cleanup_claim_token = ?", claim.MediaID, StatusCleanupPending, claim.ClaimToken).
		Updates(map[string]interface{}{
			"cleanup_claim_token": nil,
			"cleanup_claimed_at":  nil,
			"cleanup_after":       retryAt.UTC(),
			"last_cleanup_error":  errorSummary,
		})
	if result.Error != nil {
		return fmt.Errorf("schedule Post media cleanup retry: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrStaleCleanupClaim
	}
	return nil
}

func boundedCleanupError(value string) string {
	switch value {
	case CleanupErrorStorageDeleteFailure, "storage_timeout", "storage_permission_denied", "invalid_object_metadata":
	default:
		value = "storage_cleanup_failed"
	}
	return truncateCleanupError(value)
}

func truncateCleanupError(value string) string {
	if !utf8.ValidString(value) {
		value = string([]rune(value))
	}
	if len(value) <= MaxCleanupErrorBytes {
		return value
	}
	end := MaxCleanupErrorBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
