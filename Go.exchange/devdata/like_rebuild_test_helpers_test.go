package devdata

import (
	"Go.exchange/likes"
	"context"
)

func initializeLikeStore(store *likes.Store, ctx context.Context, postID uint, count, version int64, userIDs []uint) (bool, error) {
	created, err := store.InitializeFrom(ctx, postID, false, func(context.Context) (likes.FullState, error) {
		return likes.FullState{}, nil
	})
	if err != nil {
		return false, err
	}
	for _, userID := range userIDs {
		if err := store.InitializeUserEmpty(ctx, userID); err != nil {
			return false, err
		}
		if _, err := store.Mutate(ctx, userID, postID, true); err != nil {
			return false, err
		}
	}
	if count != int64(len(userIDs)) || version != int64(len(userIDs)) {
		return false, likes.ErrLikeRecoveryUnsafe
	}
	return created, nil
}
