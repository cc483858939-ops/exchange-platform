package controllers

import (
	"context"
	"slices"
	"sync"
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
)

type sharedPublicPostIDsKey struct{}

type sharedPublicPostIDs struct {
	requested []uint
	once      sync.Once
	available []uint
	err       error
}

// Only the aggregate request shares a visibility snapshot. Standalone state
// reads continue to query current eligibility, and state-specific failures
// remain independent of this common prerequisite.
func withSharedPublicPostIDs(ctx context.Context, ids []uint) context.Context {
	return context.WithValue(ctx, sharedPublicPostIDsKey{}, &sharedPublicPostIDs{requested: slices.Clone(ids)})
}

func loadPublicPostIDs(ctx context.Context, db *gorm.DB, ids []uint, now time.Time) ([]uint, error) {
	load := func() ([]uint, error) {
		var available []uint
		err := publicPostScope(db.Model(&models.Post{}), now).
			Where("posts.id IN ?", ids).
			Pluck("posts.id", &available).Error
		return available, err
	}
	shared, _ := ctx.Value(sharedPublicPostIDsKey{}).(*sharedPublicPostIDs)
	if shared == nil || !slices.Equal(shared.requested, ids) {
		return load()
	}
	shared.once.Do(func() { shared.available, shared.err = load() })
	return shared.available, shared.err
}
