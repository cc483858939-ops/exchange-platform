package devdata

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

type targetedReplacementIntegrationFixture struct {
	Current  SourceRegistry
	Baseline Snapshot
	Batch    TargetedRefreshBatch
	OldKey   string
	NewKey   string
	OldPost  SnapshotPost
	APost    SnapshotPost
	CPost    SnapshotPost
}

func newTargetedReplacementIntegrationFixture(data syncIntegrationData, now time.Time) targetedReplacementIntegrationFixture {
	oldKey := data.Registry.Accounts[1].Key
	newKey := "it_d_" + data.Tag
	data.SourceIDs[newKey] = strconv.FormatInt(data.Base+4, 10)
	current := SourceRegistry{
		Version:         SourceRegistryVersion,
		DefaultMaxPosts: DefaultMaxPosts,
		Accounts: []SourceAccount{
			data.Registry.Accounts[0],
			{Key: newKey, Platform: "x", Handle: newKey, Category: "integration", MaxPosts: DefaultMaxPosts, Enabled: true},
			data.Registry.Accounts[2],
		},
	}
	aPost := data.sourcePost(data.Registry.Accounts[0].Key, 1401, now.Add(-time.Hour), "retained A")
	oldPost := data.sourcePost(oldKey, 1402, now.Add(-2*time.Hour), "retired B")
	cPost := data.sourcePost(data.Registry.Accounts[2].Key, 1403, now.Add(-3*time.Hour), "retained C")
	newPostID := strconv.FormatInt(data.Base+1404, 10)
	newPost := SnapshotPost{
		RegistryKey:  newKey,
		SourcePostID: newPostID,
		SourceURL:    fmt.Sprintf("https://x.com/%s/status/%s", newKey, newPostID),
		Text:         "it-marker-" + data.Tag + " replacement D source content",
		CreatedAt:    now.Add(-4 * time.Hour).UTC(),
		Language:     "en",
		SourceMetrics: SourceMetrics{
			LikeCount: 4, ReplyCount: 5, RepostCount: 6, QuoteCount: 7,
		},
	}
	newAccount := SnapshotAccount{
		RegistryKey:     newKey,
		SourceUserID:    data.SourceIDs[newKey],
		Handle:          newKey,
		Name:            "Integration " + newKey,
		Description:     "Integration replacement profile " + data.Tag,
		ProfileImageURL: "https://img.example.test/" + newKey,
		Category:        "integration",
	}
	return targetedReplacementIntegrationFixture{
		Current:  current,
		Baseline: data.snapshot(now, aPost, oldPost, cPost),
		Batch: TargetedRefreshBatch{
			RegistryKey: newKey,
			FetchedAt:   now.Add(time.Minute),
			Accounts:    []SnapshotAccount{newAccount},
			Posts:       []SnapshotPost{newPost},
		},
		OldKey:  oldKey,
		NewKey:  newKey,
		OldPost: oldPost,
		APost:   aPost,
		CPost:   cPost,
	}
}

func TestDevDataTargetedReplacementSyncIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	fixture := newTargetedReplacementIntegrationFixture(data, now)

	if _, err := SyncSnapshot(context.Background(), db, data.Registry, fixture.Baseline, nil, now); err != nil {
		t.Fatalf("seed replacement baseline: %v", err)
	}
	beforeA := findMirrorAccount(t, db, data.Registry.Accounts[0].Key)
	beforeC := findMirrorAccount(t, db, data.Registry.Accounts[2].Key)
	oldBefore := findMirrorAccount(t, db, fixture.OldKey)
	oldMappingBefore := findMirrorMapping(t, db, fixture.OldPost.SourcePostID)

	result, err := SyncTargetedReplacement(context.Background(), db, fixture.Current, fixture.Baseline, fixture.OldKey, fixture.Batch, nil, now.Add(time.Minute), SyncOptions{})
	if err != nil {
		t.Fatalf("targeted replacement sync: %v", err)
	}
	if result.Inserted != 1 || result.RetiredHard != 1 || result.RetiredSoft != 0 {
		t.Fatalf("targeted replacement result=%#v", result)
	}

	oldAfter := findMirrorAccount(t, db, fixture.OldKey)
	if oldAfter.ID != oldBefore.ID || oldAfter.Enabled {
		t.Fatalf("retired mirror account=%#v", oldAfter)
	}
	var oldUser models.User
	if err := db.Unscoped().First(&oldUser, oldBefore.LocalUserID).Error; err != nil {
		t.Fatalf("load retained retired mirror user: %v", err)
	}
	if oldUser.DeletedAt.Valid || oldUser.Username != MirrorUsername(fixture.OldKey) {
		t.Fatalf("retired mirror user=%#v", oldUser)
	}
	var retiredMapping models.DevDataMirrorPost
	if err := db.Unscoped().Where("id = ?", oldMappingBefore.ID).First(&retiredMapping).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("retired mapping err=%v mapping=%#v", err, retiredMapping)
	}
	var retiredPost models.Post
	if err := db.Unscoped().First(&retiredPost, oldMappingBefore.LocalPostID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("retired Post err=%v post=%#v", err, retiredPost)
	}

	for _, want := range []struct {
		key       string
		post      SnapshotPost
		accountID uint
	}{
		{key: data.Registry.Accounts[0].Key, post: fixture.APost, accountID: beforeA.ID},
		{key: data.Registry.Accounts[2].Key, post: fixture.CPost, accountID: beforeC.ID},
	} {
		account := findMirrorAccount(t, db, want.key)
		if account.ID != want.accountID || !account.Enabled {
			t.Fatalf("retained mirror account %q=%#v", want.key, account)
		}
		mapping := findMirrorMapping(t, db, want.post.SourcePostID)
		if mapping.MirrorAccountID != account.ID || mapping.State != models.DevDataMirrorPostStateActive {
			t.Fatalf("retained mapping %q=%#v", want.key, mapping)
		}
	}

	newAccount := findMirrorAccount(t, db, fixture.NewKey)
	if !newAccount.Enabled || newAccount.SourceUserID != fixture.Batch.Accounts[0].SourceUserID || newAccount.LocalUserID == oldBefore.LocalUserID {
		t.Fatalf("new mirror account=%#v", newAccount)
	}
	var newUser models.User
	if err := db.Unscoped().First(&newUser, newAccount.LocalUserID).Error; err != nil {
		t.Fatalf("load new mirror user: %v", err)
	}
	if newUser.DeletedAt.Valid || newUser.Username != MirrorUsername(fixture.NewKey) {
		t.Fatalf("new mirror user=%#v", newUser)
	}
	newMapping := findMirrorMapping(t, db, fixture.Batch.Posts[0].SourcePostID)
	if newMapping.MirrorAccountID != newAccount.ID || newMapping.State != models.DevDataMirrorPostStateActive {
		t.Fatalf("new mapping=%#v", newMapping)
	}
}

func TestDevDataTargetedReplacementRollbackPreservesOldStateIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	fixture := newTargetedReplacementIntegrationFixture(data, now)

	if _, err := SyncSnapshot(context.Background(), db, data.Registry, fixture.Baseline, nil, now); err != nil {
		t.Fatalf("seed replacement rollback baseline: %v", err)
	}
	oldBefore := findMirrorAccount(t, db, fixture.OldKey)
	oldMappingBefore := findMirrorMapping(t, db, fixture.OldPost.SourcePostID)
	collisionUser := models.User{Username: MirrorUsername(fixture.NewKey), DisplayName: "replacement collision"}
	if err := db.Create(&collisionUser).Error; err != nil {
		t.Fatalf("create replacement collision user: %v", err)
	}

	_, err := SyncTargetedReplacement(context.Background(), db, fixture.Current, fixture.Baseline, fixture.OldKey, fixture.Batch, nil, now.Add(time.Minute), SyncOptions{})
	if !errors.Is(err, ErrMirrorUsernameCollision) {
		t.Fatalf("replacement collision error=%v", err)
	}
	oldAfter := findMirrorAccount(t, db, fixture.OldKey)
	if oldAfter.ID != oldBefore.ID || !oldAfter.Enabled {
		t.Fatalf("rollback changed old mirror account=%#v", oldAfter)
	}
	oldMappingAfter := findMirrorMapping(t, db, fixture.OldPost.SourcePostID)
	if oldMappingAfter.ID != oldMappingBefore.ID || oldMappingAfter.State != models.DevDataMirrorPostStateActive {
		t.Fatalf("rollback changed old mapping=%#v", oldMappingAfter)
	}
	var newAccount models.DevDataMirrorAccount
	if err := db.Unscoped().Where("registry_key = ?", fixture.NewKey).First(&newAccount).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("rollback left new mirror account err=%v account=%#v", err, newAccount)
	}
}
