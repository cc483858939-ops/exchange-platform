package likes

import "context"

// Fixtures use static baselines through the production token protocol.
func initializeLikeStore(store *Store, ctx context.Context, postID uint, count, version int64, userIDs []uint) (bool, error) {
	return store.InitializeFrom(ctx, postID, false, func(context.Context) (FullState, error) {
		return FullState{Count: count, Version: version, UserIDs: userIDs}, nil
	})
}

func recoverLikeStore(store *Store, ctx context.Context, postID uint, baseline FullState, fence RecoveryFence) (bool, error) {
	token, err := store.BeginRebuild(ctx, postID)
	if err != nil {
		return false, err
	}
	defer store.ReleaseRebuildMany(ctx, map[uint]string{postID: token})
	fence.RebuildToken = token
	return store.Recover(ctx, postID, baseline, fence)
}
