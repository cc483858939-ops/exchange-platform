// Package postmediacleanup owns durable removal of published user Post media.
package postmediacleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/models"
	"Go.exchange/postmedia"
	"Go.exchange/postmediaupload"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnqueueDeletedPost runs after soft deletion in the caller's transaction.
// User upload IDs are consumed once; they cannot be rebound by create-post.
// DevData folders can be regenerated/reused and are not storage GC candidates.
func EnqueueDeletedPost(tx *gorm.DB, postID, ownerID uint, now time.Time) error {
	var media []models.PostMedia
	if err := tx.Where("post_id = ?", postID).Find(&media).Error; err != nil {
		return err
	}
	jobs := make([]models.PostMediaCleanup, 0, len(media))
	for _, image := range media {
		if !strings.HasPrefix(image.URL, postmedia.FilesURLPrefix+postmedia.UserV1ObjectPrefix) {
			continue
		}
		id, keys, err := postmedia.UserDeletionKeys(ownerID, image.URL, image.LargeURL)
		if err != nil {
			return fmt.Errorf("resolve deleted Post media: %w", err)
		}
		jobs = append(jobs, models.PostMediaCleanup{
			MediaID: id, OwnerID: ownerID, MediumObjectKey: keys[0], LargeObjectKey: keys[1],
			CreatedAt: now.UTC(), CleanupAfter: now.UTC(),
		})
	}
	if len(jobs) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&jobs).Error
}

func ClaimBatch(ctx context.Context, db *gorm.DB, now time.Time, timeout time.Duration, limit int) ([]postmediaupload.CleanupClaim, error) {
	if db == nil || ctx == nil || now.IsZero() || timeout <= 0 || limit < 1 || limit > postmediaupload.MaxCleanupClaimBatchSize {
		return nil, errors.New("invalid published media cleanup claim parameters")
	}
	claims := make([]postmediaupload.CleanupClaim, 0, limit)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobs []models.PostMediaCleanup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("cleanup_after <= ?", now.UTC()).Order("cleanup_after, media_id").Limit(limit).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			token := uuid.NewString()
			result := tx.Model(&models.PostMediaCleanup{}).Where("media_id = ?", job.MediaID).
				Updates(map[string]interface{}{"claim_token": token, "cleanup_after": now.Add(timeout).UTC(), "attempts": gorm.Expr("attempts + 1")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return postmediaupload.ErrStaleCleanupClaim
			}
			claims = append(claims, postmediaupload.CleanupClaim{
				MediaID: job.MediaID, OwnerID: job.OwnerID, MediumObjectKey: job.MediumObjectKey, LargeObjectKey: job.LargeObjectKey,
				ClaimToken: token, CleanupAttempts: job.Attempts + 1, Published: true,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// Retain an object while ANY live Post still references it, even if that Post
// is not currently public. The indexed exact URL probes do not scan all Posts.
func Unreferenced(ctx context.Context, db *gorm.DB, claim postmediaupload.CleanupClaim) (bool, error) {
	if db == nil || ctx == nil {
		return false, errors.New("published media cleanup database/context is missing")
	}
	id, _, err := postmedia.UserDeletionKeys(claim.OwnerID, postmedia.PublicURL(claim.MediumObjectKey), postmedia.PublicURL(claim.LargeObjectKey))
	if err != nil || id != claim.MediaID {
		return false, errors.New("invalid published media cleanup object identity")
	}
	var referenced bool
	err = db.WithContext(ctx).Raw(`SELECT
EXISTS (SELECT 1 FROM post_media m JOIN posts p ON p.id = m.post_id WHERE m.url = ? AND p.deleted_at IS NULL)
OR EXISTS (SELECT 1 FROM post_media m JOIN posts p ON p.id = m.post_id WHERE m.large_url = ? AND p.deleted_at IS NULL)`,
		postmedia.PublicURL(claim.MediumObjectKey), postmedia.PublicURL(claim.LargeObjectKey)).Scan(&referenced).Error
	return !referenced, err
}

func Complete(ctx context.Context, db *gorm.DB, claim postmediaupload.CleanupClaim) error {
	if db == nil || ctx == nil || claim.MediaID == "" || claim.ClaimToken == "" {
		return errors.New("invalid published media completion parameters")
	}
	result := db.WithContext(ctx).Where("media_id = ? AND claim_token = ?", claim.MediaID, claim.ClaimToken).Delete(&models.PostMediaCleanup{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return postmediaupload.ErrStaleCleanupClaim
	}
	return nil
}

func Retry(ctx context.Context, db *gorm.DB, claim postmediaupload.CleanupClaim, at time.Time, reason string) error {
	if db == nil || ctx == nil || claim.MediaID == "" || claim.ClaimToken == "" || at.IsZero() {
		return errors.New("invalid published media retry parameters")
	}
	switch reason {
	case "media_still_referenced", postmediaupload.CleanupErrorStorageDeleteFailure:
	default:
		reason = "storage_cleanup_failed"
	}
	result := db.WithContext(ctx).Model(&models.PostMediaCleanup{}).
		Where("media_id = ? AND claim_token = ?", claim.MediaID, claim.ClaimToken).
		Updates(map[string]interface{}{"claim_token": nil, "cleanup_after": at.UTC(), "last_error": reason})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return postmediaupload.ErrStaleCleanupClaim
	}
	return nil
}
