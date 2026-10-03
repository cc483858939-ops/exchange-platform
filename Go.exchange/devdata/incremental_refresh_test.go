package devdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type resumableIncrementalClient struct {
	baseline    Snapshot
	calls       []string
	failHandle  string
	failure     error
	afterLookup func()
}

func (client *resumableIncrementalClient) LookupUsers(ctx context.Context, handles []string) (map[string]XUser, error) {
	users := make(map[string]XUser)
	for _, handle := range handles {
		client.calls = append(client.calls, handle)
		if handle == client.failHandle {
			return nil, client.failure
		}
		for _, account := range client.baseline.Accounts {
			if account.Handle == handle {
				protected := false
				users[strings.ToLower(handle)] = XUser{ID: account.SourceUserID, Username: handle, Name: account.Name, Protected: &protected}
			}
		}
	}
	if client.afterLookup != nil {
		client.afterLookup()
	}
	return users, ctx.Err()
}

func (client *resumableIncrementalClient) GetUserPosts(ctx context.Context, id, _ string, _ int) (XTimelinePage, error) {
	for index, account := range client.baseline.Accounts {
		if account.SourceUserID == id {
			return XTimelinePage{Posts: []XPost{{ID: fmt.Sprintf("400000000000000%03d", index), AuthorID: id, Text: "a complete resumable source post", CreatedAt: client.baseline.FetchedAt.Add(time.Hour)}}}, ctx.Err()
		}
	}
	return XTimelinePage{}, errors.New("unknown source identity")
}

func incrementalRefreshFixture(t *testing.T) (SourceRegistry, *resumableIncrementalClient, IncrementalRefreshOptions) {
	t.Helper()
	registry := incrementalTestRegistry(8)
	baseline := incrementalBaseline(registry)
	path := filepath.Join(t.TempDir(), "x_latest.json")
	if err := WriteSnapshotAtomic(path, baseline, registry); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	return registry, &resumableIncrementalClient{baseline: baseline}, IncrementalRefreshOptions{
		Source: "rsshub", Shard: "auto", FetchCount: 20, SnapshotPath: path,
		Now: func() time.Time { return now },
	}
}

func incrementalTestFiles() incrementalRefreshFiles {
	return incrementalRefreshFiles{writeCheckpoint: WriteIncrementalCheckpointAtomic, writeSnapshot: WriteIncrementalSnapshotIfUnchanged, removeCheckpoint: removeIncrementalCheckpoint}
}

func assertIncrementalCheckpointRemoved(t *testing.T, options IncrementalRefreshOptions) {
	t.Helper()
	if _, err := os.Stat(DefaultIncrementalCheckpointPath(options.SnapshotPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint still exists: %v", err)
	}
}

func TestIncrementalRefreshResumesOnlyPendingAccountsAndThenDueShard(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	selected, _ := SelectIncrementalShardAccounts(registry, IncrementalShardForTime(options.Now()))
	client.failHandle, client.failure = selected[1].Handle, errors.New("source unavailable")
	var applied []int
	apply := func(_ context.Context, batch IncrementalBatch) error {
		applied = append(applied, batch.Shard)
		return nil
	}
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err == nil {
		t.Fatal("failed account unexpectedly succeeded")
	}
	checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath))
	if err != nil || len(checkpoint.Completed) != 1 || checkpoint.Stage != IncrementalStageFetching || len(applied) != 0 {
		t.Fatalf("checkpoint=%+v applied=%v err=%v", checkpoint, applied, err)
	}
	unchanged, _ := ReadSnapshot(options.SnapshotPath, registry)
	if len(unchanged.Posts) != 0 {
		t.Fatal("partial shard changed final snapshot")
	}
	client.failHandle, client.calls = "", nil
	options.Now = func() time.Time { return checkpoint.StartedAt.Add(6 * time.Hour) }
	results, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply)
	if err != nil || len(results) != 2 || len(applied) != 2 || applied[0] != checkpoint.Shard || applied[1] != IncrementalShardForTime(options.Now()) {
		t.Fatalf("results=%+v applied=%v err=%v", results, applied, err)
	}
	if len(client.calls) != 3 || client.calls[0] != selected[1].Handle {
		t.Fatalf("resume calls=%v; expected pending account then next shard", client.calls)
	}
	for _, handle := range client.calls {
		if handle == selected[0].Handle {
			t.Fatal("completed account was refetched")
		}
	}
	next, err := ReadSnapshot(options.SnapshotPath, registry)
	if err != nil || len(next.Posts) != 4 {
		t.Fatalf("merged posts=%d err=%v", len(next.Posts), err)
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshRateLimitPreservesProgressAndCooldown(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	selected, _ := SelectIncrementalShardAccounts(registry, IncrementalShardForTime(options.Now()))
	client.failHandle, client.failure = selected[1].Handle, &RSSHubHTTPError{StatusCode: 429, RetryAfter: time.Hour}
	applyCalls := 0
	apply := func(context.Context, IncrementalBatch) error { applyCalls++; return nil }
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); !IsRSSHubRateLimitError(err) {
		t.Fatalf("rate limit error=%v", err)
	}
	checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath))
	if err != nil || len(checkpoint.Completed) != 1 || !checkpoint.RetryNotBefore.Equal(options.Now().Add(time.Hour)) {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	client.calls, client.failHandle = nil, ""
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("cooldown error=%v", err)
	}
	if len(client.calls) != 0 || applyCalls != 0 {
		t.Fatalf("cooldown performed work: calls=%v apply=%d", client.calls, applyCalls)
	}
	options.Now = func() time.Time { return checkpoint.RetryNotBefore }
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || client.calls[0] != selected[1].Handle || applyCalls != 1 {
		t.Fatalf("resume calls=%v apply=%d", client.calls, applyCalls)
	}
}

func TestIncrementalRefreshPreviousDayCheckpointDoesNotSkipNewSlotOfSameShard(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	start := options.Now()
	selected, _ := SelectIncrementalShardAccounts(registry, IncrementalShardForTime(start))
	client.failHandle, client.failure = selected[1].Handle, errors.New("source unavailable")
	var applied []int
	apply := func(_ context.Context, batch IncrementalBatch) error {
		applied = append(applied, batch.Shard)
		return nil
	}
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err == nil {
		t.Fatal("expected failed fetch")
	}
	client.failHandle, client.calls = "", nil
	options.Now = func() time.Time { return start.Add(24 * time.Hour) }
	results, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply)
	if err != nil || len(results) != 2 || len(applied) != 2 || applied[0] != applied[1] {
		t.Fatalf("results=%+v applied=%v err=%v", results, applied, err)
	}
	if len(client.calls) != 3 || client.calls[0] != selected[1].Handle || client.calls[1] != selected[0].Handle || client.calls[2] != selected[1].Handle {
		t.Fatalf("expected pending old account then both accounts in new slot: calls=%v", client.calls)
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshApplyFailureReusesCompleteBatch(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	applyErr := errors.New("transaction rolled back")
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { return applyErr }); !errors.Is(err, applyErr) {
		t.Fatalf("apply error=%v", err)
	}
	checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath))
	if err != nil || checkpoint.Stage != IncrementalStageReady || len(checkpoint.Completed) != 2 {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	client.calls = nil
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("ready batch made source requests: %v", client.calls)
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshSnapshotFailureRecoversWithoutDatabaseReplay(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	files := incrementalTestFiles()
	writeErr := errors.New("disk unavailable")
	files.writeSnapshot = func(string, string, Snapshot, SourceRegistry) error { return writeErr }
	applyCalls := 0
	apply := func(context.Context, IncrementalBatch) error { applyCalls++; return nil }
	if _, err := runIncrementalRefreshResumable(context.Background(), client, registry, options, apply, files); !errors.Is(err, writeErr) {
		t.Fatalf("snapshot error=%v", err)
	}
	checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath))
	if err != nil || checkpoint.Stage != IncrementalStageSynced {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	client.calls = nil
	results, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply)
	if err != nil || len(results) != 1 || results[0].Applied || applyCalls != 1 || len(client.calls) != 0 {
		t.Fatalf("results=%+v apply=%d calls=%v err=%v", results, applyCalls, client.calls, err)
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshCommitBeforeCheckpointFailureSafelyReplays(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	files := incrementalTestFiles()
	writeErr := errors.New("checkpoint disk unavailable")
	files.writeCheckpoint = func(path string, checkpoint IncrementalCheckpoint) error {
		if checkpoint.Stage == IncrementalStageSynced {
			return writeErr
		}
		return WriteIncrementalCheckpointAtomic(path, checkpoint)
	}
	applyCalls := 0
	apply := func(context.Context, IncrementalBatch) error { applyCalls++; return nil }
	if _, err := runIncrementalRefreshResumable(context.Background(), client, registry, options, apply, files); !errors.Is(err, writeErr) {
		t.Fatalf("checkpoint error=%v", err)
	}
	client.calls = nil
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err != nil {
		t.Fatal(err)
	}
	if applyCalls != 2 || len(client.calls) != 0 {
		t.Fatalf("saved batch was not replayed without refetch: apply=%d calls=%v", applyCalls, client.calls)
	}
}

func TestIncrementalRefreshCrashAfterSnapshotWriteOnlyRemovesCheckpoint(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	files := incrementalTestFiles()
	removeErr := errors.New("checkpoint removal interrupted")
	files.removeCheckpoint = func(string) error { return removeErr }
	applyCalls := 0
	apply := func(context.Context, IncrementalBatch) error { applyCalls++; return nil }
	if _, err := runIncrementalRefreshResumable(context.Background(), client, registry, options, apply, files); !errors.Is(err, removeErr) {
		t.Fatalf("cleanup error=%v", err)
	}
	client.calls = nil
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err != nil {
		t.Fatal(err)
	}
	if applyCalls != 1 || len(client.calls) != 0 {
		t.Fatalf("completed snapshot replayed: apply=%d calls=%v", applyCalls, client.calls)
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshRejectsChangedConfigurationOrBaseline(t *testing.T) {
	for _, change := range []string{"registry", "source", "window", "baseline", "manual-shard", "malformed"} {
		t.Run(change, func(t *testing.T) {
			registry, client, options := incrementalRefreshFixture(t)
			selected, _ := SelectIncrementalShardAccounts(registry, IncrementalShardForTime(options.Now()))
			client.failHandle, client.failure = selected[1].Handle, errors.New("source unavailable")
			applyCalls := 0
			apply := func(context.Context, IncrementalBatch) error { applyCalls++; return nil }
			if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err == nil {
				t.Fatal("expected failed fetch")
			}
			path := DefaultIncrementalCheckpointPath(options.SnapshotPath)
			switch change {
			case "registry":
				registry.Accounts[0].Category = "changed"
			case "source":
				options.Source = "x"
			case "window":
				options.FetchCount = 60
			case "baseline":
				updated := client.baseline
				updated.FetchedAt = updated.FetchedAt.Add(time.Hour)
				if err := WriteSnapshotAtomic(options.SnapshotPath, updated, registry); err != nil {
					t.Fatal(err)
				}
			case "manual-shard":
				options.Shard = "1"
			case "malformed":
				if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			client.failHandle, client.calls = "", nil
			if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, apply); err == nil {
				t.Fatal("changed state was accepted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) || len(client.calls) != 0 || applyCalls != 0 {
				t.Fatalf("conflict mutated state: calls=%v apply=%d", client.calls, applyCalls)
			}
		})
	}
}

func TestIncrementalRefreshTwentyFourHourCycleCoversEveryAccountOnce(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	start := options.Now()
	var applied []int
	for hour := 0; hour < 24; hour += 6 {
		now := start.Add(time.Duration(hour) * time.Hour)
		options.Now = func() time.Time { return now }
		if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(_ context.Context, batch IncrementalBatch) error {
			applied = append(applied, batch.Shard)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	counts := make(map[string]int)
	for _, handle := range client.calls {
		counts[handle]++
	}
	if len(applied) != 4 || len(counts) != len(registry.Accounts) {
		t.Fatalf("applied=%v account counts=%v", applied, counts)
	}
	for handle, count := range counts {
		if count != 1 {
			t.Fatalf("account %s fetched %d times", handle, count)
		}
	}
	assertIncrementalCheckpointRemoved(t, options)
}

func TestIncrementalRefreshRejectsAlteredReadySnapshot(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { return errors.New("database unavailable") }); err == nil {
		t.Fatal("expected apply failure")
	}
	path := DefaultIncrementalCheckpointPath(options.SnapshotPath)
	checkpoint, err := ReadIncrementalCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.NextSnapshot.Accounts[0].Name = "altered saved snapshot"
	if err := WriteIncrementalCheckpointAtomic(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	client.calls = nil
	_, err = RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { t.Fatal("altered snapshot applied"); return nil })
	if err == nil || !strings.Contains(err.Error(), "does not match its saved batch") || len(client.calls) != 0 {
		t.Fatalf("altered snapshot accepted or refetched: calls=%v err=%v", client.calls, err)
	}
}

func TestIncrementalRefreshSyncedRecoveryDoesNotOverwriteExternalBaseline(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	files := incrementalTestFiles()
	files.writeSnapshot = func(string, string, Snapshot, SourceRegistry) error { return errors.New("disk unavailable") }
	if _, err := runIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { return nil }, files); err == nil {
		t.Fatal("expected snapshot failure")
	}
	external := cloneSnapshot(client.baseline)
	external.Accounts[0].Name = "externally updated profile"
	if err := WriteSnapshotAtomic(options.SnapshotPath, external, registry); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(options.SnapshotPath)
	client.calls = nil
	_, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error {
		t.Fatal("synced conflict replayed database")
		return nil
	})
	after, _ := os.ReadFile(options.SnapshotPath)
	if !errors.Is(err, ErrIncrementalSnapshotChanged) || string(before) != string(after) || len(client.calls) != 0 {
		t.Fatalf("external baseline was not preserved: calls=%v err=%v", client.calls, err)
	}
	if checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath)); err != nil || checkpoint.Stage != IncrementalStageSynced {
		t.Fatalf("synced checkpoint lost: stage=%s err=%v", checkpoint.Stage, err)
	}
}

func TestIncrementalRefreshCancellationKeepsCompletedAccounts(t *testing.T) {
	registry, client, options := incrementalRefreshFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lookupCount := 0
	client.afterLookup = func() {
		lookupCount++
		if lookupCount == 2 {
			cancel()
		}
	}
	if _, err := RunIncrementalRefreshResumable(ctx, client, registry, options, func(context.Context, IncrementalBatch) error { t.Fatal("partial batch applied"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	checkpoint, err := ReadIncrementalCheckpoint(DefaultIncrementalCheckpointPath(options.SnapshotPath))
	if err != nil || len(checkpoint.Completed) != 1 {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
}

func TestIncrementalCheckpointPathsCannotOverwriteSnapshotOrFullCheckpoint(t *testing.T) {
	for _, target := range []string{"x_latest.json", "x_fetch_checkpoint.json"} {
		t.Run(target, func(t *testing.T) {
			registry, client, options := incrementalRefreshFixture(t)
			options.CheckpointPath = filepath.Join(filepath.Dir(options.SnapshotPath), target)
			if _, err := RunIncrementalRefreshResumable(context.Background(), client, registry, options, func(context.Context, IncrementalBatch) error { return nil }); err == nil {
				t.Fatal("unsafe checkpoint path accepted")
			}
			if len(client.calls) != 0 {
				t.Fatal("unsafe path caused source request")
			}
		})
	}
}
