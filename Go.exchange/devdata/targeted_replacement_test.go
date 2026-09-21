package devdata

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateReplacementBaselineAcceptsExactOneAccountTransition(t *testing.T) {
	if err := ValidateReplacementBaseline(replacementBaseline(), replacementRegistry(), RegistryReplacement{OldKey: "b", NewKey: "d"}); err != nil {
		t.Fatal(err)
	}
	registry := replacementRegistry()
	registry.Accounts = append(registry.Accounts, SourceAccount{Key: "b", Platform: "x", Handle: "b", Category: "test", MaxPosts: 3, Enabled: false})
	if err := ValidateReplacementBaseline(replacementBaseline(), registry, RegistryReplacement{OldKey: "b", NewKey: "d"}); err != nil {
		t.Fatalf("disabled old registry entry should be accepted: %v", err)
	}
}

func TestReadReplacementBaselineReturnsRawFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "x_latest.json")
	payload, err := json.Marshal(replacementBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	baseline, fingerprint, err := ReadReplacementBaseline(path, replacementRegistry(), RegistryReplacement{OldKey: "b", NewKey: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline, replacementBaseline()) {
		t.Fatalf("baseline=%#v", baseline)
	}
	if fingerprint == "" {
		t.Fatal("replacement baseline fingerprint is empty")
	}
}

func TestFetchTargetedReplacementRequestsOnlyNewHandle(t *testing.T) {
	now := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	post := targetedSourcePost("d", "400", "replacement target post", now.Add(-time.Minute))
	protected := false
	client := targetedRefreshSourceClient{
		users: map[string]XUser{
			"d": {ID: "4", Name: "D", Username: "d", Description: "replacement source", Protected: &protected},
		},
		posts: map[string][]XPost{"4": {post}},
	}
	batch, _, err := FetchTargetedReplacementAccount(context.Background(), &client, replacementRegistry(), replacementBaseline(), RegistryReplacement{OldKey: "b", NewKey: "d"}, TargetedRefreshOptions{
		FetchCount: 20,
		FetchedAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.lookupHandles, []string{"d"}) {
		t.Fatalf("lookup handles=%v, want [d]", client.lookupHandles)
	}
	if batch.RegistryKey != "d" || len(batch.Accounts) != 1 || batch.Accounts[0].SourceUserID != "4" {
		t.Fatalf("replacement batch=%#v", batch)
	}
}

func TestFetchTargetedReplacementRejectsAdditionalRegistryChangesBeforeFetch(t *testing.T) {
	registry := replacementRegistry()
	registry.Accounts = append(registry.Accounts, SourceAccount{Key: "e", Platform: "x", Handle: "e", Category: "test", MaxPosts: 3, Enabled: true})
	client := targetedRefreshSourceClient{}
	_, _, err := FetchTargetedReplacementAccount(context.Background(), &client, registry, replacementBaseline(), RegistryReplacement{OldKey: "b", NewKey: "d"}, TargetedRefreshOptions{FetchCount: 20})
	if err == nil || !strings.Contains(err.Error(), "additional new account") {
		t.Fatalf("additional registry change error=%v", err)
	}
	if len(client.lookupHandles) != 0 {
		t.Fatalf("source was queried after transition rejection: %v", client.lookupHandles)
	}
}

func TestValidateRegistryReplacementRejectsInvalidTransitions(t *testing.T) {
	tests := []struct {
		name        string
		registry    SourceRegistry
		baseline    Snapshot
		replacement RegistryReplacement
		want        string
	}{
		{
			name: "old still enabled",
			registry: SourceRegistry{
				Version: SourceRegistryVersion, DefaultMaxPosts: 3,
				Accounts: []SourceAccount{
					{Key: "a", Platform: "x", Handle: "a", Category: "test", MaxPosts: 3, Enabled: true},
					{Key: "b", Platform: "x", Handle: "b", Category: "test", MaxPosts: 3, Enabled: true},
					{Key: "c", Platform: "x", Handle: "c", Category: "test", MaxPosts: 3, Enabled: true},
					{Key: "d", Platform: "x", Handle: "d", Category: "test", MaxPosts: 3, Enabled: true},
				},
			},
			baseline: replacementBaseline(), replacement: RegistryReplacement{OldKey: "b", NewKey: "d"},
			want: "still enabled",
		},
		{
			name: "new already in baseline",
			registry: SourceRegistry{
				Version: SourceRegistryVersion, DefaultMaxPosts: 3,
				Accounts: []SourceAccount{
					{Key: "a", Platform: "x", Handle: "a", Category: "test", MaxPosts: 3, Enabled: true},
					{Key: "c", Platform: "x", Handle: "c", Category: "test", MaxPosts: 3, Enabled: true},
					{Key: "d", Platform: "x", Handle: "d", Category: "test", MaxPosts: 3, Enabled: true},
				},
			},
			baseline: func() Snapshot {
				baseline := replacementBaseline()
				baseline.Accounts = append(baseline.Accounts, targetedSnapshotAccount("d", "D", "D description", "https://img.example/d.jpg", false, ""))
				return baseline
			}(),
			replacement: RegistryReplacement{OldKey: "b", NewKey: "d"},
			want:        "already exists in baseline",
		},
		{
			name: "same key", registry: replacementRegistry(), baseline: replacementBaseline(),
			replacement: RegistryReplacement{OldKey: "b", NewKey: "b"}, want: "must differ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRegistryReplacement(test.baseline, test.registry, test.replacement.OldKey, test.replacement.NewKey)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateRegistryReplacementRejectsUnrelatedConfigurationChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*SourceRegistry)
		want   string
	}{
		{
			name: "handle",
			mutate: func(registry *SourceRegistry) {
				registry.Accounts[0].Handle = "changed_a"
			},
			want: "changed handle",
		},
		{
			name: "category",
			mutate: func(registry *SourceRegistry) {
				registry.Accounts[0].Category = "changed"
			},
			want: "changed category",
		},
		{
			name: "max_posts",
			mutate: func(registry *SourceRegistry) {
				registry.Accounts[0].MaxPosts = 2
			},
			want: "max_posts",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := replacementRegistry()
			test.mutate(&registry)
			err := ValidateRegistryReplacement(replacementBaseline(), registry, "b", "d")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestMergeReplacementSnapshotRemovesOldAccountAndPreservesRetainedData(t *testing.T) {
	baseline := replacementBaseline()
	registry := replacementRegistry()
	beforeA := baselineAccountByKey(baseline, "a")
	beforeC := baselineAccountByKey(baseline, "c")
	beforeAPost := snapshotPostByKey(baseline, "a", "100")
	beforeCPost := snapshotPostByKey(baseline, "c", "300")
	now := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	newPost := targetedSnapshotPost("d", "400", "D replacement post", now)
	next, err := MergeReplacementSnapshot(baseline, TargetedRefreshBatch{
		RegistryKey: "d", FetchedAt: now,
		Accounts: []SnapshotAccount{targetedSnapshotAccount("d", "D", "D description", "https://img.example/d.jpg", false, "")},
		Posts:    []SnapshotPost{newPost},
	}, registry, "b", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSnapshotAccount(next, "b"); ok {
		t.Fatal("retired account remained in snapshot")
	}
	if _, ok := findSnapshotPost(next, "b", "200"); ok {
		t.Fatal("retired account post remained in snapshot")
	}
	if got := baselineAccountByKey(next, "a"); !reflect.DeepEqual(got, beforeA) {
		t.Fatalf("account A changed: before=%#v after=%#v", beforeA, got)
	}
	if got := baselineAccountByKey(next, "c"); !reflect.DeepEqual(got, beforeC) {
		t.Fatalf("account C changed: before=%#v after=%#v", beforeC, got)
	}
	if got := snapshotPostByKey(next, "a", "100"); !reflect.DeepEqual(got, beforeAPost) {
		t.Fatalf("post A changed: before=%#v after=%#v", beforeAPost, got)
	}
	if got := snapshotPostByKey(next, "c", "300"); !reflect.DeepEqual(got, beforeCPost) {
		t.Fatalf("post C changed: before=%#v after=%#v", beforeCPost, got)
	}
	if _, ok := findSnapshotAccount(next, "d"); !ok {
		t.Fatal("new account missing from snapshot")
	}
	if _, ok := findSnapshotPost(next, "d", "400"); !ok {
		t.Fatal("new post missing from snapshot")
	}
	if err := ValidateSnapshot(next, registry); err != nil {
		t.Fatalf("strict final validation failed: %v", err)
	}
}

func TestMergeReplacementSnapshotRejectsNewSourceUserCollision(t *testing.T) {
	baseline := replacementBaseline()
	newAccount := targetedSnapshotAccount("d", "D", "D description", "https://img.example/d.jpg", false, "")
	newAccount.SourceUserID = baselineAccountByKey(baseline, "a").SourceUserID
	_, err := MergeReplacementSnapshot(baseline, TargetedRefreshBatch{
		RegistryKey: "d", FetchedAt: time.Now().UTC(), Accounts: []SnapshotAccount{newAccount},
	}, replacementRegistry(), "b", time.Now().UTC())
	if err == nil || !errors.Is(err, ErrSourceIdentityMismatch) {
		t.Fatalf("source collision error=%v", err)
	}
}

func TestReplacementProfileMediaPreparationScopesToNewKey(t *testing.T) {
	registry := replacementRegistry()
	baseline := replacementBaseline()
	now := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)
	newAccount := targetedSnapshotAccount("d", "D", "D description", "https://img.example/d.jpg", true, "https://banner.example/d.jpg")
	batch := TargetedRefreshBatch{
		RegistryKey: "d",
		FetchedAt:   now,
		Accounts:    []SnapshotAccount{newAccount},
	}
	next, err := MergeReplacementSnapshot(baseline, batch, registry, "b", now)
	if err != nil {
		t.Fatal(err)
	}

	body := avatarJPEGFixture(t)
	avatarFetcher := fakeAvatarFetcher{items: map[string]fakeAvatarFetch{
		newAccount.ProfileImageURL: {avatar: downloadedAvatar(body)},
	}}
	avatarResolutions, avatarReport, err := PrepareAvatarMirrorsForKeys(context.Background(), registry, next, []string{"d"}, avatarFetcher, newFakeAvatarStore())
	if err != nil {
		t.Fatalf("PrepareAvatarMirrorsForKeys: %v", err)
	}
	if avatarReport.Attempted != 1 || avatarReport.Failed != 0 || len(avatarResolutions) != 1 {
		t.Fatalf("avatar report=%#v resolutions=%#v", avatarReport, avatarResolutions)
	}

	coverFetcher := fakeCoverFetcher{items: map[string]fakeCoverFetch{
		newAccount.ProfileBannerURL: {cover: DownloadedCover{Body: body}},
	}}
	coverResolutions, coverReport, err := PrepareCoverMirrorsForKeys(context.Background(), registry, next, []string{"d"}, coverFetcher, newFakeAvatarStore())
	if err != nil {
		t.Fatalf("PrepareCoverMirrorsForKeys: %v", err)
	}
	if coverReport.Attempted != 1 || coverReport.Failed != 0 || len(coverResolutions) != 1 {
		t.Fatalf("cover report=%#v resolutions=%#v", coverReport, coverResolutions)
	}
}

func replacementRegistry() SourceRegistry {
	return SourceRegistry{
		Version: SourceRegistryVersion, DefaultMaxPosts: 3,
		Accounts: []SourceAccount{
			{Key: "a", Platform: "x", Handle: "a", Category: "test", MaxPosts: 3, Enabled: true},
			{Key: "d", Platform: "x", Handle: "d", Category: "test", MaxPosts: 3, Enabled: true},
			{Key: "c", Platform: "x", Handle: "c", Category: "test", MaxPosts: 3, Enabled: true},
		},
	}
}

func replacementBaseline() Snapshot {
	return Snapshot{
		Version: DefaultSnapshotVersion, FetchedAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		Accounts: []SnapshotAccount{
			targetedSnapshotAccount("a", "A", "A description", "https://img.example/a.jpg", false, ""),
			targetedSnapshotAccount("b", "B", "B description", "https://img.example/b.jpg", false, ""),
			targetedSnapshotAccount("c", "C", "C description", "https://img.example/c.jpg", true, "https://banner.example/c.jpg"),
		},
		Posts: []SnapshotPost{
			targetedSnapshotPost("a", "100", "A baseline post", time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)),
			targetedSnapshotPost("b", "200", "B baseline post", time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)),
			targetedSnapshotPost("c", "300", "C baseline post", time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)),
		},
	}
}

func findSnapshotAccount(snapshot Snapshot, key string) (SnapshotAccount, bool) {
	for _, account := range snapshot.Accounts {
		if account.RegistryKey == key {
			return account, true
		}
	}
	return SnapshotAccount{}, false
}
