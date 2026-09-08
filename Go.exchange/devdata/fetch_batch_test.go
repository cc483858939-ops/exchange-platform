package devdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type checkpointTestSourceClient struct {
	lookupHandles []string
	failHandles   map[string]int
	rateHandles   map[string]int
	rateAfter     time.Duration
	attempts      map[string]int
}

func (client *checkpointTestSourceClient) LookupUsers(_ context.Context, handles []string) (map[string]XUser, error) {
	if client.attempts == nil {
		client.attempts = make(map[string]int)
	}
	if len(handles) == 0 {
		return nil, errors.New("empty test lookup")
	}
	handle := strings.TrimSpace(handles[0])
	client.lookupHandles = append(client.lookupHandles, handle)
	client.attempts[strings.ToLower(handle)]++
	if remaining := client.rateHandles[strings.ToLower(handle)]; remaining > 0 {
		client.rateHandles[strings.ToLower(handle)] = remaining - 1
		return nil, &RSSHubHTTPError{StatusCode: 429, RetryAfter: client.rateAfter}
	}
	if remaining := client.failHandles[strings.ToLower(handle)]; remaining > 0 {
		client.failHandles[strings.ToLower(handle)] = remaining - 1
		return nil, errors.New("synthetic account failure")
	}
	protected := false
	index, parseErr := strconv.Atoi(strings.TrimPrefix(strings.ToLower(handle), "source"))
	if parseErr != nil {
		index = len(client.lookupHandles)
	}
	return map[string]XUser{strings.ToLower(handle): {
		ID:        fmt.Sprintf("%d", 1001+index),
		Name:      handle,
		Username:  handle,
		Protected: &protected,
	}}, nil
}

func (*checkpointTestSourceClient) GetUserPosts(context.Context, string, string, int) (XTimelinePage, error) {
	return XTimelinePage{}, nil
}

func batchTestRegistry(count int) SourceRegistry {
	accounts := make([]SourceAccount, 0, count)
	for index := 0; index < count; index++ {
		handle := fmt.Sprintf("source%d", index)
		accounts = append(accounts, SourceAccount{
			Key:      handle,
			Platform: "x",
			Handle:   handle,
			Category: "test",
			MaxPosts: 1,
			Enabled:  true,
		})
	}
	return SourceRegistry{Version: SourceRegistryVersion, DefaultMaxPosts: 1, Accounts: accounts}
}

func TestFetchRSSHubResumableSavesEachSuccessAndResumesOnlyPending(t *testing.T) {
	registry := batchTestRegistry(4)
	directory := t.TempDir()
	checkpointPath := filepath.Join(directory, "checkpoint.json")
	snapshotPath := filepath.Join(directory, "snapshot.json")
	firstClient := &checkpointTestSourceClient{failHandles: map[string]int{"source1": 1}}
	options := ResumableFetchOptions{
		BatchSize:      4,
		BatchDelay:     0,
		CheckpointPath: checkpointPath,
		SnapshotPath:   snapshotPath,
		Now:            func() time.Time { return testFetchedAt() },
	}
	_, _, err := FetchRSSHubResumable(context.Background(), firstClient, registry, options)
	var incomplete *FetchIncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error=%v", err)
	}
	checkpoint, err := ReadFetchCheckpoint(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Completed) != 3 || len(checkpoint.Failures) != 1 {
		t.Fatalf("checkpoint completed=%d failures=%d", len(checkpoint.Completed), len(checkpoint.Failures))
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial snapshot stat error=%v", err)
	}
	if got := strings.Join(firstClient.lookupHandles, ","); got != "source0,source1,source2,source3" {
		t.Fatalf("first lookup sequence=%q", got)
	}

	secondClient := &checkpointTestSourceClient{}
	snapshot, report, err := FetchRSSHubResumable(context.Background(), secondClient, registry, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Accounts) != 4 || len(report.PerAccount) != 4 {
		t.Fatalf("snapshot accounts=%d report accounts=%d", len(snapshot.Accounts), len(report.PerAccount))
	}
	if got := strings.Join(secondClient.lookupHandles, ","); got != "source1" {
		t.Fatalf("resume lookup sequence=%q", got)
	}
	if _, err := os.Stat(checkpointPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint was not removed: %v", err)
	}
	if _, err := ReadSnapshot(snapshotPath, registry); err != nil {
		t.Fatalf("final snapshot invalid: %v", err)
	}
}

func TestFetchRSSHubResumablePreservesExistingSnapshotOnIncompleteRun(t *testing.T) {
	registry := batchTestRegistry(2)
	directory := t.TempDir()
	checkpointPath := filepath.Join(directory, "checkpoint.json")
	snapshotPath := filepath.Join(directory, "snapshot.json")
	options := ResumableFetchOptions{BatchSize: 2, CheckpointPath: checkpointPath, SnapshotPath: snapshotPath, BatchDelay: 0}
	if _, _, err := FetchRSSHubResumable(context.Background(), &checkpointTestSourceClient{}, registry, options); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	client := &checkpointTestSourceClient{failHandles: map[string]int{"source1": 1}}
	if _, _, err := FetchRSSHubResumable(context.Background(), client, registry, options); err == nil {
		t.Fatal("incomplete fetch unexpectedly succeeded")
	}
	after, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("existing final snapshot changed during incomplete fetch")
	}
}

func TestFetchRSSHubResumablePacesPendingAccountsByBatch(t *testing.T) {
	registry := batchTestRegistry(12)
	directory := t.TempDir()
	var waits []time.Duration
	var progress []string
	_, _, err := FetchRSSHubResumable(context.Background(), &checkpointTestSourceClient{}, registry, ResumableFetchOptions{
		BatchSize:      5,
		BatchDelay:     7 * time.Second,
		CheckpointPath: filepath.Join(directory, "checkpoint.json"),
		SnapshotPath:   filepath.Join(directory, "snapshot.json"),
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
		Progress: func(message string) { progress = append(progress, message) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 2 || waits[0] != 7*time.Second || waits[1] != 7*time.Second {
		t.Fatalf("waits=%#v", waits)
	}
	joined := strings.Join(progress, "\n")
	for _, want := range []string{"batch=1/3", "batch=2/3", "batch=3/3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress missing %q: %s", want, joined)
		}
	}
}

func TestFetchRSSHubResumableStopsImmediatelyOn429(t *testing.T) {
	registry := batchTestRegistry(6)
	directory := t.TempDir()
	var waits []time.Duration
	var progress []string
	client := &checkpointTestSourceClient{
		rateHandles: map[string]int{"source2": 1},
		rateAfter:   120 * time.Second,
	}
	_, _, err := FetchRSSHubResumable(context.Background(), client, registry, ResumableFetchOptions{
		BatchSize:      5,
		BatchDelay:     7 * time.Second,
		CheckpointPath: filepath.Join(directory, "checkpoint.json"),
		SnapshotPath:   filepath.Join(directory, "snapshot.json"),
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
		Progress: func(message string) { progress = append(progress, message) },
	})
	if !errors.Is(err, ErrFetchIncomplete) {
		t.Fatalf("error=%v", err)
	}
	if len(waits) != 0 {
		t.Fatalf("429 triggered waits=%v", waits)
	}
	if got := strings.Join(client.lookupHandles, ","); got != "source0,source1,source2" {
		t.Fatalf("lookup sequence=%q", got)
	}
	checkpoint, err := ReadFetchCheckpoint(filepath.Join(directory, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Completed) != 2 {
		t.Fatalf("completed=%d", len(checkpoint.Completed))
	}
	failure, exists := checkpoint.Failures["source2"]
	if !exists || failure.Attempts != 1 || !strings.Contains(failure.LastError, "HTTP 429") {
		t.Fatalf("source2 failure=%#v", failure)
	}
	for _, handle := range []string{"source3", "source4", "source5"} {
		if _, attempted := client.attempts[handle]; attempted {
			t.Fatalf("account after rate limit was attempted: %s", handle)
		}
	}
	if strings.Contains(strings.ToLower(strings.Join(progress, "\n")), "retry") {
		t.Fatalf("progress advertised retry: %v", progress)
	}
}

func TestFetchRSSHubResumable429PreservesExistingSnapshotAndManualResume(t *testing.T) {
	registry := batchTestRegistry(6)
	directory := t.TempDir()
	checkpointPath := filepath.Join(directory, "checkpoint.json")
	snapshotPath := filepath.Join(directory, "snapshot.json")
	options := ResumableFetchOptions{
		BatchSize:      5,
		BatchDelay:     7 * time.Second,
		CheckpointPath: checkpointPath,
		SnapshotPath:   snapshotPath,
	}
	if _, _, err := FetchRSSHubResumable(context.Background(), &checkpointTestSourceClient{}, registry, options); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}

	var waits []time.Duration
	firstClient := &checkpointTestSourceClient{
		rateHandles: map[string]int{"source2": 1},
		rateAfter:   120 * time.Second,
	}
	options.Wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	if _, _, err := FetchRSSHubResumable(context.Background(), firstClient, registry, options); !errors.Is(err, ErrFetchIncomplete) {
		t.Fatalf("first invocation error=%v", err)
	}
	if len(waits) != 0 {
		t.Fatalf("429 triggered waits=%v", waits)
	}
	after, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("existing final snapshot changed during 429 fetch")
	}
	checkpoint, err := ReadFetchCheckpoint(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Completed) != 2 || checkpoint.Failures["source2"].Attempts != 1 {
		t.Fatalf("checkpoint completed=%d failure=%#v", len(checkpoint.Completed), checkpoint.Failures["source2"])
	}

	secondClient := &checkpointTestSourceClient{}
	if _, _, err := FetchRSSHubResumable(context.Background(), secondClient, registry, options); err != nil {
		t.Fatalf("resume error=%v", err)
	}
	if got := strings.Join(secondClient.lookupHandles, ","); got != "source2,source3,source4,source5" {
		t.Fatalf("resume lookup sequence=%q", got)
	}
	if _, err := os.Stat(checkpointPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint was not removed: %v", err)
	}
	if _, err := ReadSnapshot(snapshotPath, registry); err != nil {
		t.Fatalf("final snapshot invalid: %v", err)
	}
}

func TestPreflightRSSHubSourcesStopsImmediatelyOn429(t *testing.T) {
	registry := batchTestRegistry(6)
	client := &checkpointTestSourceClient{
		rateHandles: map[string]int{"source1": 1},
		rateAfter:   120 * time.Second,
	}
	var waits []time.Duration
	results, err := PreflightRSSHubSources(context.Background(), client, registry, ResumableFetchOptions{
		BatchSize:  5,
		BatchDelay: 9 * time.Second,
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	})
	if !errors.Is(err, ErrPreflightFailed) {
		t.Fatalf("error=%v", err)
	}
	if len(results) != 6 || len(waits) != 0 {
		t.Fatalf("results=%d waits=%v", len(results), waits)
	}
	if results[0].ProfileStatus != "ok" || results[1].ProfileStatus != "rate_limited" {
		t.Fatalf("rate-limit results=%#v", results[:2])
	}
	for index := 2; index < len(results); index++ {
		if results[index].ProfileStatus != "not_attempted" {
			t.Fatalf("result[%d]=%#v", index, results[index])
		}
	}
	if got := strings.Join(client.lookupHandles, ","); got != "source0,source1" {
		t.Fatalf("lookup sequence=%q", got)
	}
}

func TestPreflightRSSHubSourcesPacesBatches(t *testing.T) {
	registry := batchTestRegistry(12)
	var waits []time.Duration
	results, err := PreflightRSSHubSources(context.Background(), &checkpointTestSourceClient{}, registry, ResumableFetchOptions{
		BatchSize:  5,
		BatchDelay: 7 * time.Second,
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(waits) != fmt.Sprint([]time.Duration{7 * time.Second, 7 * time.Second}) {
		t.Fatalf("waits=%v", waits)
	}
	for index, result := range results {
		if result.ProfileStatus != "ok" || result.Error != "" {
			t.Fatalf("result[%d]=%#v", index, result)
		}
	}
}

func TestFetchCheckpointAtomicReadAndFingerprintValidation(t *testing.T) {
	registry := batchTestRegistry(1)
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	checkpoint := FetchCheckpoint{
		Version:             FetchCheckpointVersion,
		Source:              "rsshub",
		RegistryFingerprint: RegistryFingerprint(registry),
		StartedAt:           testFetchedAt(),
		UpdatedAt:           testFetchedAt(),
		Completed:           map[string]FetchAccountData{},
		Failures:            map[string]FetchFailure{},
	}
	if err := WriteFetchCheckpointAtomic(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFetchCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != checkpoint.Version || got.RegistryFingerprint != checkpoint.RegistryFingerprint {
		t.Fatalf("checkpoint=%#v", got)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("checkpoint permissions=%o", info.Mode().Perm())
	}
	changed := registry
	changed.Accounts[0].Category = "changed"
	if err := ValidateFetchCheckpoint(got, changed, "rsshub"); err == nil || !strings.Contains(err.Error(), "does not match current source registry") {
		t.Fatalf("fingerprint mismatch error=%v", err)
	}
	if err := RemoveFetchCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFetchCheckpoint(path); err != nil {
		t.Fatal(err)
	}
}

func TestReadFetchCheckpointRejectsInvalidJSONAndVersion(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "checkpoint.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFetchCheckpoint(path); err == nil {
		t.Fatal("invalid JSON unexpectedly accepted")
	}
	if err := os.WriteFile(path, []byte(`{"version":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFetchCheckpoint(path); err == nil || !strings.Contains(err.Error(), "unsupported fetch checkpoint version") {
		t.Fatalf("version error=%v", err)
	}
}

func TestFetchRSSHubResumableCancellationDuringWaitPreservesCheckpoint(t *testing.T) {
	registry := batchTestRegistry(6)
	directory := t.TempDir()
	checkpointPath := filepath.Join(directory, "checkpoint.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := FetchRSSHubResumable(ctx, &checkpointTestSourceClient{}, registry, ResumableFetchOptions{
		BatchSize:      5,
		BatchDelay:     time.Minute,
		CheckpointPath: checkpointPath,
		SnapshotPath:   filepath.Join(directory, "snapshot.json"),
		Wait: func(_ context.Context, _ time.Duration) error {
			cancel()
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	checkpoint, err := ReadFetchCheckpoint(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Completed) != 5 {
		t.Fatalf("completed=%d", len(checkpoint.Completed))
	}
}

func testFetchedAt() time.Time {
	return time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
}
