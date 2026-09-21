package devdata

import (
	"context"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"
	"Go.exchange/profilecoverimage"
)

func TestDevDataCoverMirrorLifecycleIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	accountKey := data.Registry.Accounts[0].Key

	first := data.snapshot(now)
	first.Accounts[0].ProfileBannerPresent = true
	first.Accounts[0].ProfileBannerURL = "https://pbs.twimg.com/profile_banners/a.jpg"
	derivativeA, err := profilecoverimage.Optimize(avatarJPEGFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	objectA, err := BuildCoverObjectKey(accountKey, derivativeA.ContentHash, derivativeA.Extension)
	if err != nil {
		t.Fatal(err)
	}
	resolutionA := CoverResolution{RegistryKey: accountKey, SourceURL: first.Accounts[0].ProfileBannerURL, ObjectKey: objectA, LocalURL: coverLocalURL(objectA), ContentHash: derivativeA.ContentHash}
	options := SyncOptions{CoverResolutions: map[string]CoverResolution{accountKey: resolutionA}, PreserveExistingCoverWhenUnresolved: true}
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, first, nil, now, options); err != nil {
		t.Fatalf("initial cover sync: %v", err)
	}
	account := findMirrorAccount(t, db, accountKey)
	var user models.User
	if err := db.First(&user, account.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.CoverImageURL != resolutionA.LocalURL || account.SourceCoverURL != resolutionA.SourceURL || account.CoverObjectKey != objectA || account.CoverContentHash != derivativeA.ContentHash {
		t.Fatalf("initial cover state user=%#v account=%#v", user, account)
	}

	failed := first
	failed.FetchedAt = now.Add(time.Minute)
	failed.Accounts = append([]SnapshotAccount(nil), first.Accounts...)
	failed.Accounts[0].ProfileBannerURL = "https://pbs.twimg.com/profile_banners/b.jpg"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, failed, nil, failed.FetchedAt, SyncOptions{PreserveExistingCoverWhenUnresolved: true}); err != nil {
		t.Fatalf("failed replacement sync: %v", err)
	}
	account = findMirrorAccount(t, db, accountKey)
	if err := db.First(&user, account.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.CoverImageURL != resolutionA.LocalURL || account.SourceCoverURL != failed.Accounts[0].ProfileBannerURL || account.CoverObjectKey != objectA || account.CoverContentHash != derivativeA.ContentHash {
		t.Fatalf("failed replacement state user=%#v account=%#v", user, account)
	}

	second := failed
	derivativeB, err := profilecoverimage.Optimize(avatarPNGFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	objectB, err := BuildCoverObjectKey(accountKey, derivativeB.ContentHash, derivativeB.Extension)
	if err != nil {
		t.Fatal(err)
	}
	resolutionB := CoverResolution{RegistryKey: accountKey, SourceURL: second.Accounts[0].ProfileBannerURL, ObjectKey: objectB, LocalURL: coverLocalURL(objectB), ContentHash: derivativeB.ContentHash}
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, second, nil, now.Add(2*time.Minute), SyncOptions{CoverResolutions: map[string]CoverResolution{accountKey: resolutionB}, PreserveExistingCoverWhenUnresolved: true}); err != nil {
		t.Fatalf("successful replacement sync: %v", err)
	}
	account = findMirrorAccount(t, db, accountKey)
	if err := db.First(&user, account.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.CoverImageURL != resolutionB.LocalURL || account.SourceCoverURL != resolutionB.SourceURL || account.CoverObjectKey != objectB || account.CoverContentHash != derivativeB.ContentHash {
		t.Fatalf("successful replacement state user=%#v account=%#v", user, account)
	}

	removed := second
	removed.FetchedAt = now.Add(3 * time.Minute)
	removed.Accounts = append([]SnapshotAccount(nil), second.Accounts...)
	removed.Accounts[0].ProfileBannerURL = ""
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, removed, nil, removed.FetchedAt, SyncOptions{PreserveExistingCoverWhenUnresolved: true}); err != nil {
		t.Fatalf("explicit removal sync: %v", err)
	}
	account = findMirrorAccount(t, db, accountKey)
	if err := db.First(&user, account.LocalUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.CoverImageURL != "" || account.SourceCoverURL != "" || account.CoverObjectKey != "" || account.CoverContentHash != "" {
		t.Fatalf("explicit removal state user=%#v account=%#v", user, account)
	}
	if strings.Contains(user.CoverImageURL, "pbs.twimg.com") {
		t.Fatal("mirror user retained raw remote cover URL")
	}
}
