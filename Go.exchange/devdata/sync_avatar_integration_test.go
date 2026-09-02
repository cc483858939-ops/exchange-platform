package devdata

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"Go.exchange/models"
)

func TestDevDataMirrorAvatarSyncOptionsLifecycleIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	root := data.sourcePost(data.Registry.Accounts[0].Key, 2201, now.Add(-time.Hour), "avatar-root")
	snapshot := data.snapshot(now, root)
	aBody := avatarJPEGFixture(t)
	aResolution := integrationAvatarResolution(t, snapshot.Accounts[0], aBody)

	first, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, snapshot, nil, now, SyncOptions{
		AvatarResolutions: map[string]AvatarResolution{snapshot.Accounts[0].RegistryKey: aResolution},
	})
	if err != nil {
		t.Fatalf("first avatar sync: %v", err)
	}
	if first.Inserted != 1 {
		t.Fatalf("first result=%#v", first)
	}

	accountA := findMirrorAccount(t, db, snapshot.Accounts[0].RegistryKey)
	accountB := findMirrorAccount(t, db, snapshot.Accounts[1].RegistryKey)
	var userA, userB models.User
	if err := db.First(&userA, accountA.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&userB, accountB.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if userA.AvatarURL != aResolution.LocalURL || accountA.AvatarObjectKey != aResolution.ObjectKey || accountA.AvatarContentHash != aResolution.ContentHash {
		t.Fatalf("localized account=%#v user=%#v", accountA, userA)
	}
	if userB.AvatarURL != snapshot.Accounts[1].ProfileImageURL || accountB.AvatarObjectKey != "" || accountB.AvatarContentHash != "" {
		t.Fatalf("fallback account=%#v user=%#v", accountB, userB)
	}

	changed := snapshot
	changed.Accounts = append([]SnapshotAccount(nil), snapshot.Accounts...)
	changed.Accounts[0].ProfileImageURL = "https://img.example.test/new-avatar-a"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, changed, nil, now.Add(time.Minute), SyncOptions{
		PreserveExistingAvatarWhenUnresolved: true,
	}); err != nil {
		t.Fatalf("preserve-on-failure sync: %v", err)
	}
	if err := db.First(&userA, accountA.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if userA.AvatarURL != aResolution.LocalURL {
		t.Fatalf("existing localized avatar was not preserved: %q", userA.AvatarURL)
	}
	accountA = findMirrorAccount(t, db, snapshot.Accounts[0].RegistryKey)
	if accountA.SourceAvatarURL != changed.Accounts[0].ProfileImageURL || accountA.AvatarObjectKey != aResolution.ObjectKey || accountA.AvatarContentHash != aResolution.ContentHash {
		t.Fatalf("source metadata or successful metadata changed on failure: %#v", accountA)
	}

	newBody := avatarPNGFixture(t)
	newResolution := integrationAvatarResolution(t, changed.Accounts[0], newBody)
	updated, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, changed, nil, now.Add(2*time.Minute), SyncOptions{
		AvatarResolutions:                    map[string]AvatarResolution{changed.Accounts[0].RegistryKey: newResolution},
		PreserveExistingAvatarWhenUnresolved: true,
	})
	if err != nil {
		t.Fatalf("avatar replacement sync: %v", err)
	}
	if err := db.First(&userA, accountA.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	mapping := findMirrorMapping(t, db, root.SourcePostID)
	if userA.AvatarURL != newResolution.LocalURL || newResolution.ContentHash == aResolution.ContentHash || !containsUint(updated.AffectedPostIDs, mapping.LocalPostID) {
		t.Fatalf("avatar replacement result=%#v user=%#v mapping=%#v", updated, userA, mapping)
	}
}

func integrationAvatarResolution(t *testing.T, source SnapshotAccount, body []byte) AvatarResolution {
	t.Helper()
	hash := sha256.Sum256(body)
	contentHash := hexHash(hash)
	_, extension, ok := detectAvatarImageType(body)
	if !ok {
		t.Fatal("invalid avatar fixture")
	}
	objectKey, err := BuildAvatarObjectKey(source.RegistryKey, contentHash, extension)
	if err != nil {
		t.Fatal(err)
	}
	return AvatarResolution{
		RegistryKey: source.RegistryKey,
		SourceURL:   source.ProfileImageURL,
		ObjectKey:   objectKey,
		LocalURL:    avatarLocalURL(objectKey),
		ContentHash: contentHash,
	}
}
