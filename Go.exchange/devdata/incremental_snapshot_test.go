package devdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMergeIncrementalSnapshotKeepsCompleteRollingInventory(t *testing.T) {
	registry := incrementalTestRegistry(4)
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectIncrementalShardAccounts(registry, 0)
	if err != nil || len(selected) != 1 {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	target := selected[0]
	now := time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC)
	old := incrementalSnapshotPost(target, "310000000000000001", now.Add(-2*time.Hour))
	nonSelected := registry.EnabledAccounts()[0]
	if nonSelected.Key == target.Key {
		nonSelected = registry.EnabledAccounts()[1]
	}
	untouched := incrementalSnapshotPost(nonSelected, "310000000000000002", now.Add(-3*time.Hour))
	baseline := incrementalBaseline(registry, old, untouched)
	updated := old
	updated.Text = "updated source representation"
	updated.CreatedAt = now.Add(-time.Minute)
	newPost := incrementalSnapshotPost(target, "310000000000000003", now)
	batch := IncrementalBatch{
		FetchedAt: now,
		Shard:     assignments[target.Key],
		Accounts:  []SnapshotAccount{baselineAccount(t, baseline, target.Key)},
		Posts:     []SnapshotPost{updated, newPost},
	}
	next, err := MergeIncrementalSnapshot(baseline, batch, registry, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Accounts) != len(registry.EnabledAccounts()) {
		t.Fatalf("next accounts=%d want=%d", len(next.Accounts), len(registry.EnabledAccounts()))
	}
	if len(next.Posts) != 3 {
		t.Fatalf("next posts=%d want=3: %#v", len(next.Posts), next.Posts)
	}
	if got, ok := findSnapshotPost(next, target.Key, old.SourcePostID); !ok || got.Text != updated.Text {
		t.Fatalf("updated post=%#v exists=%v", got, ok)
	}
	if _, ok := findSnapshotPost(next, target.Key, newPost.SourcePostID); !ok {
		t.Fatal("new post was not merged")
	}
	if got, ok := findSnapshotPost(next, nonSelected.Key, untouched.SourcePostID); !ok || got.Text != untouched.Text {
		t.Fatalf("non-selected post changed or disappeared: %#v exists=%v", got, ok)
	}
	if err := ValidateSnapshot(next, registry); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIncrementalBatchRejectsOutsideShardAndDuplicates(t *testing.T) {
	registry := incrementalTestRegistry(4)
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectIncrementalShardAccounts(registry, 0)
	if err != nil || len(selected) != 1 {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	target := selected[0]
	baseline := incrementalBaseline(registry)
	batch := IncrementalBatch{
		FetchedAt: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC),
		Shard:     assignments[target.Key],
		Accounts:  []SnapshotAccount{baselineAccount(t, baseline, target.Key), baselineAccount(t, baseline, target.Key)},
	}
	if err := ValidateIncrementalBatch(batch, registry); err == nil || !strings.Contains(err.Error(), "duplicate account") {
		t.Fatalf("duplicate batch error=%v", err)
	}
	other := registry.EnabledAccounts()[0]
	if other.Key == target.Key {
		other = registry.EnabledAccounts()[1]
	}
	batch.Accounts = []SnapshotAccount{baselineAccount(t, baseline, other.Key)}
	if err := ValidateIncrementalBatch(batch, registry); err == nil || !strings.Contains(err.Error(), "outside shard") {
		t.Fatalf("outside-shard batch error=%v", err)
	}
	batch.Accounts = []SnapshotAccount{baselineAccount(t, baseline, target.Key)}
	duplicate := incrementalSnapshotPost(target, "320000000000000001", batch.FetchedAt)
	batch.Posts = []SnapshotPost{duplicate, duplicate}
	if err := ValidateIncrementalBatch(batch, registry); err == nil || !strings.Contains(err.Error(), "duplicate source Post ID") {
		t.Fatalf("duplicate post batch error=%v", err)
	}
	batch.Posts = []SnapshotPost{incrementalSnapshotPost(other, "320000000000000002", batch.FetchedAt)}
	if err := ValidateIncrementalBatch(batch, registry); err == nil || !strings.Contains(err.Error(), "unknown account") {
		t.Fatalf("outside-shard post batch error=%v", err)
	}
}

func TestReadIncrementalBaselineAndFingerprintUseExactSnapshotBytes(t *testing.T) {
	registry := testRegistry()
	directory := t.TempDir()
	path := filepath.Join(directory, "x_latest.json")
	snapshot := testSnapshot("a valid baseline source Post")
	if err := WriteSnapshotAtomic(path, snapshot, registry); err != nil {
		t.Fatal(err)
	}
	loaded, fingerprint, err := ReadIncrementalBaseline(path, registry)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Posts[0].SourcePostID != snapshot.Posts[0].SourcePostID || fingerprint == "" {
		t.Fatalf("loaded=%#v fingerprint=%q", loaded, fingerprint)
	}
	want := sha256Hex(readFileBytes(t, path))
	if fingerprint != want {
		t.Fatalf("fingerprint=%q want=%q", fingerprint, want)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadIncrementalBaseline(path, registry); err == nil || !strings.Contains(err.Error(), "valid full baseline snapshot") {
		t.Fatalf("invalid baseline error=%v", err)
	}
}

func TestReadIncrementalBaselineRejectsPartialSnapshot(t *testing.T) {
	registry := testRegistry()
	directory := t.TempDir()
	path := filepath.Join(directory, "x_latest.json")
	payload := []byte(`{"version":"nexus_x_mirror_v1","fetched_at":"2026-01-02T00:00:00Z","accounts":[],"posts":[]}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadIncrementalBaseline(path, registry); err == nil || !strings.Contains(err.Error(), "valid full baseline snapshot") {
		t.Fatalf("partial baseline error=%v", err)
	}
}

func TestReadIncrementalBaselineRejectsRegistryMismatch(t *testing.T) {
	registry := testRegistry()
	directory := t.TempDir()
	path := filepath.Join(directory, "x_latest.json")
	if err := WriteSnapshotAtomic(path, testSnapshot("a valid baseline source Post"), registry); err != nil {
		t.Fatal(err)
	}
	changed := registry
	changed.Accounts = append([]SourceAccount(nil), registry.Accounts...)
	changed.Accounts[0].Category = "obsolete"
	if _, _, err := ReadIncrementalBaseline(path, changed); err == nil || !strings.Contains(err.Error(), "valid full baseline snapshot") {
		t.Fatalf("registry mismatch error=%v", err)
	}
}

func baselineAccount(t *testing.T, snapshot Snapshot, key string) SnapshotAccount {
	t.Helper()
	for _, account := range snapshot.Accounts {
		if account.RegistryKey == key {
			return account
		}
	}
	t.Fatalf("missing baseline account %q", key)
	return SnapshotAccount{}
}

func findSnapshotPost(snapshot Snapshot, accountKey, sourceID string) (SnapshotPost, bool) {
	for _, post := range snapshot.Posts {
		if post.RegistryKey == accountKey && post.SourcePostID == sourceID {
			return post, true
		}
	}
	return SnapshotPost{}, false
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
