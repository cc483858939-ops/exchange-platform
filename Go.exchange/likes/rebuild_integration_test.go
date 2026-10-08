package likes

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitForTombstoneExpiry(t *testing.T, store *Store, postID uint) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := store.client.Exists(ReadyKey(postID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tombstone did not expire")
}

func TestRebuildRejectsPreDeletionBaselineAfterTombstoneExpiresIntegration(t *testing.T) {
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "")
	t.Setenv("LIKE_DELETION_TOMBSTONE_TTL", "1s")
	client, store, postID := openRecoverableStoreIntegration(t)
	token, err := store.BeginRebuild(t.Context(), postID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a SQL baseline read while the Post is still active.
	baseline := FullState{}
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.PTTL(ReadyKey(postID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("successful cleanup TTL=%v err=%v", ttl, err)
	}
	waitForTombstoneExpiry(t, store, postID)
	if created, err := store.Recover(t.Context(), postID, baseline, RecoveryFence{AllowZeroBootstrap: true, RebuildToken: token}); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("stale zero recovery: created=%t err=%v", created, err)
	}
	if created, err := store.Initialize(t.Context(), postID, 0, 0, nil, token); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("stale initializer: created=%t err=%v", created, err)
	}
	if exists, err := client.Exists(ReadyKey(postID), CountKey(postID), VersionKey(postID), RebuildTokenKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("state resurrected: exists=%d err=%v", exists, err)
	}
	// A fresh attempt reads SQL after acquiring its token and rejects deletion.
	if created, err := store.InitializeFrom(t.Context(), postID, false, func(context.Context) (FullState, error) { return FullState{}, ErrPostLikeUnavailable }); created || !errors.Is(err, ErrPostLikeUnavailable) {
		t.Fatalf("fresh deleted load: created=%t err=%v", created, err)
	}
}

func TestDeletionCleanupFailureKeepsFenceAndRevokesTokenIntegration(t *testing.T) {
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "true")
	t.Setenv("LIKE_DELETION_TOMBSTONE_TTL", "1s")
	client, store, postID := openRecoverableStoreIntegration(t)
	token, err := store.BeginRebuild(t.Context(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(CountKey(postID), "wrong type").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("cleanup fault=%v", err)
	}
	if ttl, err := client.TTL(ReadyKey(postID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("failed cleanup lost persistent fence: ttl=%v err=%v", ttl, err)
	}
	if exists, err := client.Exists(RebuildTokenKey(postID)).Result(); err != nil || exists != 0 {
		t.Fatalf("token not revoked: exists=%d err=%v token=%q", exists, err, token)
	}
	client.Del(CountKey(postID))
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.PTTL(ReadyKey(postID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("retry TTL=%v err=%v", ttl, err)
	}
	// Another delayed deletion attempt must preserve the already armed TTL,
	// even if unrelated corruption makes its subsequent cleanup fail.
	if err := client.LPush(CountKey(postID), "wrong type").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePost(t.Context(), postID); !errors.Is(err, ErrLikeRedisType) {
		t.Fatalf("duplicate cleanup fault=%v", err)
	}
	if ttl, err := client.PTTL(ReadyKey(postID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("duplicate made fence permanent: ttl=%v err=%v", ttl, err)
	}
}

func TestExpiredRebuildTokenCannotWriteOrReleaseNewOwnerIntegration(t *testing.T) {
	t.Setenv("LIKE_REBUILD_TOKEN_TTL", "30s")
	client, store, postID := openRecoverableStoreIntegration(t)
	oldToken, err := store.BeginRebuild(t.Context(), postID)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise actual Redis expiry, independent of request timeouts.
	if err := client.PExpire(RebuildTokenKey(postID), time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		exists, err := client.Exists(RebuildTokenKey(postID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebuild token did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	newToken, err := store.BeginRebuild(t.Context(), postID)
	if err != nil || newToken == oldToken {
		t.Fatalf("new owner=%q err=%v", newToken, err)
	}
	defer store.ReleaseRebuildMany(t.Context(), map[uint]string{postID: newToken})
	if err := store.ReleaseRebuild(t.Context(), postID, oldToken); err != nil {
		t.Fatal(err)
	}
	if current, err := client.Get(RebuildTokenKey(postID)).Result(); err != nil || current != newToken {
		t.Fatalf("new owner removed: current=%q err=%v", current, err)
	}
	if created, err := store.Initialize(t.Context(), postID, 0, 0, nil, oldToken); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("expired owner wrote: created=%t err=%v", created, err)
	}
}

func TestExplicitReactivationUsesCurrentBaselineAndIsRevocableIntegration(t *testing.T) {
	_, store, postID := openRecoverableStoreIntegration(t)
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if created, err := store.InitializeFrom(t.Context(), postID, true, func(context.Context) (FullState, error) {
		return FullState{Count: 1, Version: 3, UserIDs: []uint{11}}, nil
	}); err != nil || !created {
		t.Fatalf("confirmed active reactivation: created=%t err=%v", created, err)
	}
	if err := store.DeletePost(t.Context(), postID); err != nil {
		t.Fatal(err)
	}
	if created, err := store.InitializeFrom(t.Context(), postID, true, func(context.Context) (FullState, error) {
		if err := store.DeletePost(t.Context(), postID); err != nil {
			t.Fatal(err)
		}
		return FullState{Count: 1, Version: 3, UserIDs: []uint{11}}, nil
	}); created || !errors.Is(err, ErrLikeRecoveryFenceLost) {
		t.Fatalf("deleted during reactivation: created=%t err=%v", created, err)
	}
}
