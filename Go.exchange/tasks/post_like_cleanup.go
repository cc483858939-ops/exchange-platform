package tasks

import (
	"context"
	"log"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"gorm.io/gorm"
)

// Deletion propagation is independent of the registry sweep. A failed Redis
// operation keeps durable work due for a later pass; it never acknowledges it.
func startPostLikeCleanup(ctx context.Context, wg interface {
	Add(int)
	Done()
}) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			passCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := runPostLikeCleanupPass(passCtx, global.WorkerDb, likes.NewStore(global.RedisDB).DeletePost, time.Now().UTC())
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("[PostLikeCleanup] propagate deletion: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runPostLikeCleanupPass(ctx context.Context, db *gorm.DB, remove func(context.Context, uint) error, now time.Time) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	var pending []models.PostLikeCleanup
	if err := db.WithContext(ctx).Where("retry_after <= ?", now).Order("retry_after, post_id").Limit(100).Find(&pending).Error; err != nil {
		return err
	}
	for _, work := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := remove(ctx, work.PostID); err != nil {
			// Back off the failed front item, letting other deletions progress next
			// pass. Preserve it through outages and process restarts.
			if updateErr := db.WithContext(ctx).Model(&work).Where("retry_after = ?", work.RetryAfter).Update("retry_after", now.Add(5*time.Second)).Error; updateErr != nil {
				return updateErr
			}
			return err
		}
		if err := db.WithContext(ctx).Delete(&work).Error; err != nil {
			return err
		}
	}
	return nil
}
