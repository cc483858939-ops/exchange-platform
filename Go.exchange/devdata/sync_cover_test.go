package devdata

import (
	"strings"
	"testing"

	"Go.exchange/models"
)

func TestCoverSyncMetadataAbsentPreservesExistingState(t *testing.T) {
	existing := &models.User{CoverImageURL: "/api/files/profile-covers/devdata/v1/mkbhd/" + strings.Repeat("a", 64) + ".jpg"}
	source := SnapshotAccount{RegistryKey: "MKBHD", ProfileBannerPresent: false}
	if got := sourceCoverURLForSync(existing, source, SyncOptions{}); got != existing.CoverImageURL {
		t.Fatalf("cover URL=%q want=%q", got, existing.CoverImageURL)
	}
	if updates := coverMetadataUpdatesForSync(source, SyncOptions{}); updates != nil {
		t.Fatalf("metadata absent produced updates=%#v", updates)
	}
}

func TestCoverSyncExplicitRemovalClearsState(t *testing.T) {
	existing := &models.User{CoverImageURL: "/api/files/profile-covers/devdata/v1/mkbhd/" + strings.Repeat("a", 64) + ".jpg"}
	source := SnapshotAccount{RegistryKey: "MKBHD", ProfileBannerPresent: true}
	if got := sourceCoverURLForSync(existing, source, SyncOptions{}); got != "" {
		t.Fatalf("cover URL=%q want empty", got)
	}
	updates := coverMetadataUpdatesForSync(source, SyncOptions{})
	for _, key := range []string{"source_cover_url", "cover_object_key", "cover_content_hash"} {
		if value, ok := updates[key]; !ok || value != "" {
			t.Fatalf("%s update=%#v", key, value)
		}
	}
}

func TestCoverSyncSuccessfulReplacementAndFailedReplacementPreserveOldCover(t *testing.T) {
	oldLocal := "/api/files/profile-covers/devdata/v1/mkbhd/" + strings.Repeat("a", 64) + ".jpg"
	existing := &models.User{CoverImageURL: oldLocal}
	source := SnapshotAccount{RegistryKey: "MKBHD", ProfileBannerPresent: true, ProfileBannerURL: "https://pbs.twimg.com/profile_banners/new.jpg"}
	newHash := strings.Repeat("b", 64)
	newObject, err := BuildCoverObjectKey(source.RegistryKey, newHash, ".png")
	if err != nil {
		t.Fatal(err)
	}
	resolution := CoverResolution{
		RegistryKey: source.RegistryKey,
		SourceURL:   source.ProfileBannerURL,
		ObjectKey:   newObject,
		LocalURL:    coverLocalURL(newObject),
		ContentHash: newHash,
	}
	options := SyncOptions{CoverResolutions: map[string]CoverResolution{source.RegistryKey: resolution}, PreserveExistingCoverWhenUnresolved: true}
	if got := sourceCoverURLForSync(existing, source, options); got != resolution.LocalURL {
		t.Fatalf("successful cover URL=%q want=%q", got, resolution.LocalURL)
	}
	updates := coverMetadataUpdatesForSync(source, options)
	if updates["source_cover_url"] != source.ProfileBannerURL || updates["cover_object_key"] != newObject || updates["cover_content_hash"] != newHash {
		t.Fatalf("successful metadata updates=%#v", updates)
	}

	failedSource := source
	failedSource.ProfileBannerURL = "https://pbs.twimg.com/profile_banners/failed.jpg"
	failedURL := sourceCoverURLForSync(existing, failedSource, SyncOptions{PreserveExistingCoverWhenUnresolved: true})
	if failedURL != oldLocal {
		t.Fatalf("failed replacement cover URL=%q want=%q", failedURL, oldLocal)
	}
	failedUpdates := coverMetadataUpdatesForSync(failedSource, SyncOptions{PreserveExistingCoverWhenUnresolved: true})
	if failedUpdates["source_cover_url"] != failedSource.ProfileBannerURL || len(failedUpdates) != 1 {
		t.Fatalf("failed replacement metadata updates=%#v", failedUpdates)
	}
}

func TestNewMirrorCoverNeverFallsBackToRemoteURL(t *testing.T) {
	source := SnapshotAccount{RegistryKey: "MKBHD", ProfileBannerPresent: true, ProfileBannerURL: "https://pbs.twimg.com/profile_banners/new.jpg"}
	if got := sourceCoverURLForSync(nil, source, SyncOptions{PreserveExistingCoverWhenUnresolved: true}); got != "" {
		t.Fatalf("new-user unresolved cover=%q, want empty", got)
	}
}
