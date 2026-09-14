package devdata

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func incrementalIntegrationTarget(t *testing.T, data syncIntegrationData) (SourceAccount, int) {
	t.Helper()
	for shard := 0; shard < IncrementalShardCount; shard++ {
		selected, err := SelectIncrementalShardAccounts(data.Registry, shard)
		if err != nil {
			t.Fatal(err)
		}
		if len(selected) > 0 {
			return selected[0], shard
		}
	}
	t.Fatal("integration registry did not produce a selected account")
	return SourceAccount{}, 0
}

func TestDevDataIncrementalSyncIsIdempotentScopedAndNonRetiringIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	target, shard := incrementalIntegrationTarget(t, data)
	var other SourceAccount
	for _, account := range data.Registry.EnabledAccounts() {
		if account.Key != target.Key {
			other = account
			break
		}
	}
	if other.Key == "" {
		t.Fatal("integration registry did not produce a non-selected account")
	}

	updated := data.sourcePost(target.Key, 1401, now.Add(-time.Hour), "incremental-updated")
	absent := data.sourcePost(target.Key, 1402, now.Add(-2*time.Hour), "incremental-absent")
	nonSelectedPost := data.sourcePost(other.Key, 1403, now.Add(-3*time.Hour), "incremental-non-selected")
	initial := data.snapshot(now, updated, absent, nonSelectedPost)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed incremental inventory: %v", err)
	}
	updatedMapping := findMirrorMapping(t, db, updated.SourcePostID)
	absentMapping := findMirrorMapping(t, db, absent.SourcePostID)
	nonSelectedAccountBefore := findMirrorAccount(t, db, other.Key)
	var nonSelectedUserBefore models.User
	if err := db.Unscoped().First(&nonSelectedUserBefore, nonSelectedAccountBefore.LocalUserID).Error; err != nil {
		t.Fatalf("load non-selected mirror user: %v", err)
	}
	var nativeBefore models.Post
	if err := db.First(&nativeBefore, updatedMapping.LocalPostID).Error; err != nil {
		t.Fatalf("load seeded local Post: %v", err)
	}
	nativeBefore.LikeCount = 17
	nativeBefore.ReplyCount = 8
	nativeBefore.ViewCount = 31
	nativeBefore.LikeSyncVersion = 9
	if err := db.Model(&models.Post{}).Where("id = ?", nativeBefore.ID).Updates(map[string]interface{}{
		"like_count":        nativeBefore.LikeCount,
		"reply_count":       nativeBefore.ReplyCount,
		"view_count":        nativeBefore.ViewCount,
		"like_sync_version": nativeBefore.LikeSyncVersion,
	}).Error; err != nil {
		t.Fatalf("set local engagement: %v", err)
	}

	accountUpdate := baselineAccount(t, initial, target.Key)
	accountUpdate.Name = "Updated " + target.Key
	accountUpdate.Description = "incremental profile update"
	updated.Text += " with a fresh source representation"
	updated.CreatedAt = now.Add(-30 * time.Minute)
	updated.SourceMetrics = SourceMetrics{LikeCount: 99, ReplyCount: 98, RepostCount: 97, QuoteCount: 96}
	newPost := data.sourcePost(target.Key, 1404, now.Add(-15*time.Minute), "incremental-new")
	batch := IncrementalBatch{
		FetchedAt: now.Add(time.Minute), Shard: shard,
		Accounts: []SnapshotAccount{accountUpdate},
		Posts:    []SnapshotPost{updated, newPost},
	}
	first, err := SyncIncremental(context.Background(), db, data.Registry, batch, nil, now.Add(time.Minute), SyncOptions{})
	if err != nil {
		t.Fatalf("first incremental sync: %v", err)
	}
	if first.Inserted != 1 || first.Kept != 1 || first.RetiredSoft != 0 || first.RetiredHard != 0 {
		t.Fatalf("first incremental result=%#v", first)
	}
	updatedAfter := findMirrorMapping(t, db, updated.SourcePostID)
	newAfter := findMirrorMapping(t, db, newPost.SourcePostID)
	if updatedAfter.ID != updatedMapping.ID || updatedAfter.LocalPostID != updatedMapping.LocalPostID || newAfter.LocalPostID == 0 {
		t.Fatalf("mapping identities changed or new mapping missing: old=%#v updated=%#v new=%#v", updatedMapping, updatedAfter, newAfter)
	}
	var nativeAfter models.Post
	if err := db.First(&nativeAfter, updatedAfter.LocalPostID).Error; err != nil {
		t.Fatalf("load updated local Post: %v", err)
	}
	if nativeAfter.Content != updated.Text || nativeAfter.LikeCount != nativeBefore.LikeCount || nativeAfter.ReplyCount != nativeBefore.ReplyCount || nativeAfter.ViewCount != nativeBefore.ViewCount || nativeAfter.LikeSyncVersion != nativeBefore.LikeSyncVersion {
		t.Fatalf("incremental sync changed canonical/native fields unexpectedly: before=%#v after=%#v", nativeBefore, nativeAfter)
	}
	var absentAfter models.Post
	if err := db.Unscoped().First(&absentAfter, absentMapping.LocalPostID).Error; err != nil {
		t.Fatalf("load absent source Post: %v", err)
	}
	absentMappingAfter := findMirrorMapping(t, db, absent.SourcePostID)
	if absentMappingAfter.ID != absentMapping.ID || absentMappingAfter.State != models.DevDataMirrorPostStateActive || absentAfter.DeletedAt.Valid {
		t.Fatalf("absent source Post was retired: mapping=%#v Post=%#v", absentMappingAfter, absentAfter)
	}
	nonSelectedAccountAfter := findMirrorAccount(t, db, other.Key)
	var nonSelectedUserAfter models.User
	if err := db.Unscoped().First(&nonSelectedUserAfter, nonSelectedAccountAfter.LocalUserID).Error; err != nil {
		t.Fatalf("reload non-selected mirror user: %v", err)
	}
	lastFetchedSame := nonSelectedAccountAfter.LastFetchedAt == nil && nonSelectedAccountBefore.LastFetchedAt == nil
	if nonSelectedAccountAfter.LastFetchedAt != nil && nonSelectedAccountBefore.LastFetchedAt != nil {
		lastFetchedSame = nonSelectedAccountAfter.LastFetchedAt.Equal(*nonSelectedAccountBefore.LastFetchedAt)
	}
	if nonSelectedAccountAfter.ID != nonSelectedAccountBefore.ID || nonSelectedAccountAfter.RegistryKey != nonSelectedAccountBefore.RegistryKey || nonSelectedAccountAfter.Platform != nonSelectedAccountBefore.Platform || nonSelectedAccountAfter.SourceUserID != nonSelectedAccountBefore.SourceUserID || nonSelectedAccountAfter.SourceHandle != nonSelectedAccountBefore.SourceHandle || nonSelectedAccountAfter.LocalUserID != nonSelectedAccountBefore.LocalUserID || nonSelectedAccountAfter.Category != nonSelectedAccountBefore.Category || nonSelectedAccountAfter.Enabled != nonSelectedAccountBefore.Enabled || nonSelectedAccountAfter.SourceAvatarURL != nonSelectedAccountBefore.SourceAvatarURL || nonSelectedAccountAfter.AvatarObjectKey != nonSelectedAccountBefore.AvatarObjectKey || nonSelectedAccountAfter.AvatarContentHash != nonSelectedAccountBefore.AvatarContentHash || !lastFetchedSame || nonSelectedAccountAfter.CreatedAt != nonSelectedAccountBefore.CreatedAt || nonSelectedAccountAfter.UpdatedAt != nonSelectedAccountBefore.UpdatedAt || nonSelectedUserAfter.DisplayName != nonSelectedUserBefore.DisplayName || nonSelectedUserAfter.Bio != nonSelectedUserBefore.Bio || nonSelectedUserAfter.AvatarURL != nonSelectedUserBefore.AvatarURL {
		t.Fatalf("non-selected account changed before=%#v after=%#v userBefore=%#v userAfter=%#v", nonSelectedAccountBefore, nonSelectedAccountAfter, nonSelectedUserBefore, nonSelectedUserAfter)
	}

	second, err := SyncIncremental(context.Background(), db, data.Registry, batch, nil, now.Add(2*time.Minute), SyncOptions{})
	if err != nil {
		t.Fatalf("replayed incremental sync: %v", err)
	}
	if second.Inserted != 0 || second.Kept != 2 || second.RetiredSoft != 0 || second.RetiredHard != 0 {
		t.Fatalf("replayed incremental result=%#v", second)
	}
	if replayed := findMirrorMapping(t, db, newPost.SourcePostID); replayed.ID != newAfter.ID || replayed.LocalPostID != newAfter.LocalPostID {
		t.Fatalf("replay created a duplicate mapping: before=%#v after=%#v", newAfter, replayed)
	}
}

func TestPrepareIncrementalPostMediaMirrorsScopesResolvedAndUnresolvedPostsIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)
	target, shard := incrementalIntegrationTarget(t, data)
	resolved := data.sourcePost(target.Key, 1501, now.Add(-time.Hour), "incremental-media-resolved")
	resolved.HasMedia = true
	resolved.Media = []SnapshotMedia{{Type: "image", SourceURL: "https://pbs.twimg.com/media/incremental-resolved.jpg"}}
	unresolved := data.sourcePost(target.Key, 1502, now.Add(-2*time.Hour), "incremental-media-unresolved")
	unresolved.HasMedia = true
	unresolved.Media = []SnapshotMedia{{Type: "image", SourceURL: "https://pbs.twimg.com/media/incremental-unresolved.jpg"}}
	initial := data.snapshot(now, resolved, unresolved)
	resolution := integrationPostMediaResolution(t, resolved, 0, avatarJPEGFixture(t))
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, initial, nil, now, SyncOptions{
		PostMediaResolutions: map[SourcePostKey][]PostMediaResolution{{RegistryKey: resolved.RegistryKey, SourcePostID: resolved.SourcePostID}: {resolution}},
	}); err != nil {
		t.Fatalf("seed media inventory: %v", err)
	}

	newPost := data.sourcePost(target.Key, 1503, now.Add(-30*time.Minute), "incremental-media-new")
	newPost.HasMedia = true
	newPost.Media = []SnapshotMedia{{Type: "image", SourceURL: "https://pbs.twimg.com/media/incremental-new.jpg"}}
	account := baselineAccount(t, initial, target.Key)
	batch := IncrementalBatch{
		FetchedAt: now.Add(time.Minute), Shard: shard,
		Accounts: []SnapshotAccount{account},
		Posts:    []SnapshotPost{resolved, unresolved, newPost},
	}
	body := avatarJPEGFixture(t)
	fetcher := &fakePostMediaFetcher{items: map[string]fakePostMediaFetch{
		unresolved.Media[0].SourceURL: {media: downloadedPostMedia(body)},
		newPost.Media[0].SourceURL:    {media: downloadedPostMedia(body)},
	}, calls: make(map[string]int)}
	store := newFakeAvatarStore()
	resolutions, report, err := PrepareIncrementalPostMediaMirrors(context.Background(), db, data.Registry, batch, fetcher, store)
	if err != nil {
		t.Fatalf("prepare scoped incremental media: %v", err)
	}
	if report.PostsWithMedia != 2 || report.Attempted != 2 || report.Uploaded != 2 || report.Failed != 0 {
		t.Fatalf("scoped media report=%#v", report)
	}
	if fetcher.calls[resolved.Media[0].SourceURL] != 0 || fetcher.calls[unresolved.Media[0].SourceURL] != 1 || fetcher.calls[newPost.Media[0].SourceURL] != 1 {
		t.Fatalf("media download scope=%#v", fetcher.calls)
	}
	if len(resolutions[SourcePostKey{RegistryKey: resolved.RegistryKey, SourcePostID: resolved.SourcePostID}]) != 0 || len(resolutions[SourcePostKey{RegistryKey: unresolved.RegistryKey, SourcePostID: unresolved.SourcePostID}]) != 1 || len(resolutions[SourcePostKey{RegistryKey: newPost.RegistryKey, SourcePostID: newPost.SourcePostID}]) != 1 {
		t.Fatalf("scoped media resolutions=%#v", resolutions)
	}
	if _, err := SyncIncremental(context.Background(), db, data.Registry, batch, nil, now.Add(time.Minute), SyncOptions{PostMediaResolutions: resolutions}); err != nil {
		t.Fatalf("sync scoped incremental media: %v", err)
	}
	assertIntegrationPostMediaCountAndPositions(t, db, findMirrorMapping(t, db, resolved.SourcePostID).LocalPostID, 1)
	assertIntegrationPostMediaCountAndPositions(t, db, findMirrorMapping(t, db, unresolved.SourcePostID).LocalPostID, 1)
	assertIntegrationPostMediaCountAndPositions(t, db, findMirrorMapping(t, db, newPost.SourcePostID).LocalPostID, 1)
}

func TestDevDataMutationLockSkipsConcurrentSessionIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	secondDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open second PostgreSQL session: %v", err)
	}
	secondSQL, err := secondDB.DB()
	if err != nil {
		t.Fatalf("get second PostgreSQL session: %v", err)
	}
	t.Cleanup(func() { _ = secondSQL.Close() })

	first, acquired, err := TryAcquireDevDataMutationLock(context.Background(), db)
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%t err=%v", acquired, err)
	}
	second, secondAcquired, err := TryAcquireDevDataMutationLock(context.Background(), secondDB)
	if err != nil {
		_ = first.Release(context.Background())
		t.Fatalf("second lock attempt: %v", err)
	}
	if secondAcquired || second != nil {
		_ = first.Release(context.Background())
		if second != nil {
			_ = second.Release(context.Background())
		}
		t.Fatal("second PostgreSQL session unexpectedly acquired the held lock")
	}
	if err := first.Release(context.Background()); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	third, thirdAcquired, err := TryAcquireDevDataMutationLock(context.Background(), secondDB)
	if err != nil || !thirdAcquired || third == nil {
		t.Fatalf("lock was not reusable after release acquired=%t err=%v", thirdAcquired, err)
	}
	if err := third.Release(context.Background()); err != nil {
		t.Fatalf("release reusable lock: %v", err)
	}
}
