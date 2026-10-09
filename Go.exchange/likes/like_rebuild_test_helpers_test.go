package likes

import "context"

// Fixtures use static baselines through the production token protocol.
func initializeLikeStore(store *Store, ctx context.Context, postID uint, count, version int64, userIDs []uint) (bool, error) {
	created, err := store.InitializeFrom(ctx, postID, false, func(context.Context) (FullState, error) {
		return FullState{}, nil
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
	if count != int64(len(userIDs)) {
		if err := store.client.WithContext(ctx).Set(CountKey(postID), count, 0).Err(); err != nil {
			return false, err
		}
	}
	if version != int64(len(userIDs)) {
		if err := store.client.WithContext(ctx).Set(VersionKey(postID), version, 0).Err(); err != nil {
			return false, err
		}
	}
	return created, nil
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
