package controllers

import (
	"Go.exchange/likes"
	"context"
)

func initializeLikeStore(store *likes.Store, ctx context.Context, postID uint, count, version int64, userIDs []uint) (bool, error) {
	return store.InitializeFrom(ctx, postID, false, func(context.Context) (likes.FullState, error) {
		return likes.FullState{Count: count, Version: version, UserIDs: userIDs}, nil
	})
}
