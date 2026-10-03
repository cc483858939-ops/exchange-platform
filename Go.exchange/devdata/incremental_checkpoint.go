package devdata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	IncrementalCheckpointVersion = "x_incremental_checkpoint_v1"
	IncrementalStageFetching     = "fetching"
	IncrementalStageReady        = "ready"
	IncrementalStageSynced       = "synced"
)

type IncrementalCompletedAccount struct {
	Data   FetchAccountData         `json:"data"`
	Report IncrementalAccountReport `json:"report"`
}

// IncrementalCheckpoint is separate from the full-fetch checkpoint. Ready
// work retains its exact next snapshot; synced work can finish file persistence
// without making more source requests or repeating a known committed sync.
type IncrementalCheckpoint struct {
	Version             string                                 `json:"version"`
	Source              string                                 `json:"source"`
	RegistryFingerprint string                                 `json:"registry_fingerprint"`
	BaselineFingerprint string                                 `json:"baseline_fingerprint"`
	Shard               int                                    `json:"shard"`
	FetchCount          int                                    `json:"fetch_count"`
	Stage               string                                 `json:"stage"`
	StartedAt           time.Time                              `json:"started_at"`
	UpdatedAt           time.Time                              `json:"updated_at"`
	RetryNotBefore      time.Time                              `json:"retry_not_before,omitempty"`
	Completed           map[string]IncrementalCompletedAccount `json:"completed"`
	NextSnapshot        *Snapshot                              `json:"next_snapshot,omitempty"`
}

func DefaultIncrementalCheckpointPath(snapshotPath string) string {
	return filepath.Join(filepath.Dir(snapshotPath), "x_incremental_checkpoint.json")
}

func ReadIncrementalCheckpoint(path string) (IncrementalCheckpoint, error) {
	file, err := os.Open(path)
	if err != nil {
		return IncrementalCheckpoint{}, fmt.Errorf("open incremental checkpoint: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return IncrementalCheckpoint{}, fmt.Errorf("stat incremental checkpoint: %w", err)
	}
	if info.Size() > maxFetchCheckpointBytes {
		return IncrementalCheckpoint{}, errors.New("incremental checkpoint exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxFetchCheckpointBytes+1))
	decoder.DisallowUnknownFields()
	var checkpoint IncrementalCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return IncrementalCheckpoint{}, fmt.Errorf("decode incremental checkpoint: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return IncrementalCheckpoint{}, errors.New("incremental checkpoint contains invalid trailing JSON")
	}
	if err := validateIncrementalCheckpointShape(checkpoint); err != nil {
		return IncrementalCheckpoint{}, err
	}
	return checkpoint, nil
}

func WriteIncrementalCheckpointAtomic(path string, checkpoint IncrementalCheckpoint) error {
	if err := validateIncrementalCheckpointShape(checkpoint); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("encode incremental checkpoint: %w", err)
	}
	payload = append(payload, '\n')
	if len(payload) > maxFetchCheckpointBytes {
		return errors.New("incremental checkpoint exceeds size limit")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create incremental checkpoint directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".x_incremental_checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create incremental checkpoint temporary file: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict incremental checkpoint: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write incremental checkpoint: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync incremental checkpoint: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close incremental checkpoint: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace incremental checkpoint: %w", err)
	}
	return nil
}

func validateIncrementalCheckpointShape(checkpoint IncrementalCheckpoint) error {
	if checkpoint.Version != IncrementalCheckpointVersion {
		return fmt.Errorf("unsupported incremental checkpoint version %q", checkpoint.Version)
	}
	if checkpoint.Source != "rsshub" && checkpoint.Source != "x" {
		return errors.New("incremental checkpoint source must be rsshub or x")
	}
	if !isLowerHexHash(checkpoint.RegistryFingerprint) || !isLowerHexHash(checkpoint.BaselineFingerprint) {
		return errors.New("incremental checkpoint fingerprints are invalid")
	}
	if checkpoint.Shard < 0 || checkpoint.Shard >= IncrementalShardCount || checkpoint.FetchCount < 5 || checkpoint.FetchCount > DefaultRSSHubFullFetchCount {
		return errors.New("incremental checkpoint shard or fetch count is invalid")
	}
	if checkpoint.StartedAt.IsZero() || checkpoint.UpdatedAt.IsZero() || checkpoint.Completed == nil {
		return errors.New("incremental checkpoint timestamps and completed accounts are required")
	}
	switch checkpoint.Stage {
	case IncrementalStageFetching:
		if checkpoint.NextSnapshot != nil {
			return errors.New("fetching incremental checkpoint must not contain a next snapshot")
		}
	case IncrementalStageReady, IncrementalStageSynced:
		if checkpoint.NextSnapshot == nil {
			return errors.New("ready or synced incremental checkpoint requires a next snapshot")
		}
	default:
		return fmt.Errorf("invalid incremental checkpoint stage %q", checkpoint.Stage)
	}
	return nil
}

func ValidateIncrementalCheckpoint(checkpoint IncrementalCheckpoint, registry SourceRegistry, source string, fetchCount int) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	if err := validateIncrementalCheckpointShape(checkpoint); err != nil {
		return err
	}
	if checkpoint.Source != source || checkpoint.FetchCount != fetchCount || checkpoint.RegistryFingerprint != RegistryFingerprint(registry) {
		return errors.New("incremental checkpoint configuration changed; preserve the checkpoint and reconcile it before retrying")
	}
	selected, err := SelectIncrementalShardAccounts(registry, checkpoint.Shard)
	if err != nil {
		return err
	}
	byKey := make(map[string]SourceAccount, len(selected))
	for _, account := range selected {
		byKey[account.Key] = account
	}
	for key, completed := range checkpoint.Completed {
		account, ok := byKey[key]
		if !ok || completed.Data.Account.RegistryKey != key || completed.Data.Report.RegistryKey != key || completed.Report.RegistryKey != key {
			return fmt.Errorf("incremental checkpoint account %q is inconsistent or outside its shard", key)
		}
		report := completed.Report
		if report.FetchCount < checkpoint.FetchCount || report.FetchCount > DefaultRSSHubFullFetchCount || report.APIRequests < 0 || report.SourcePostsReturned < 0 || report.SourcePostsScanned < 0 || report.EligibleSelected != len(completed.Data.Posts) {
			return fmt.Errorf("incremental checkpoint account %q has an invalid report", key)
		}
		fragment := Snapshot{Version: DefaultSnapshotVersion, FetchedAt: checkpoint.StartedAt, Accounts: []SnapshotAccount{completed.Data.Account}, Posts: completed.Data.Posts}
		subset := SourceRegistry{Version: registry.Version, DefaultMaxPosts: registry.DefaultMaxPosts, Accounts: []SourceAccount{account}}
		if err := ValidateSnapshot(fragment, subset); err != nil {
			return fmt.Errorf("invalid incremental checkpoint account %q: %w", key, err)
		}
	}
	if checkpoint.Stage != IncrementalStageFetching {
		if _, _, err := incrementalBatchFromCheckpoint(checkpoint, registry); err != nil {
			return err
		}
		if err := ValidateSnapshot(*checkpoint.NextSnapshot, registry); err != nil {
			return fmt.Errorf("invalid incremental checkpoint next snapshot: %w", err)
		}
	}
	return nil
}

func incrementalBatchFromCheckpoint(checkpoint IncrementalCheckpoint, registry SourceRegistry) (IncrementalBatch, IncrementalFetchReport, error) {
	selected, err := SelectIncrementalShardAccounts(registry, checkpoint.Shard)
	if err != nil {
		return IncrementalBatch{}, IncrementalFetchReport{}, err
	}
	batch := IncrementalBatch{FetchedAt: checkpoint.StartedAt, Shard: checkpoint.Shard}
	report := IncrementalFetchReport{Shard: checkpoint.Shard, FetchCount: checkpoint.FetchCount, Accounts: len(selected)}
	for _, account := range selected {
		completed, ok := checkpoint.Completed[account.Key]
		if !ok {
			return IncrementalBatch{}, report, fmt.Errorf("incremental checkpoint is incomplete: missing %q", account.Key)
		}
		batch.Accounts = append(batch.Accounts, completed.Data.Account)
		batch.Posts = append(batch.Posts, completed.Data.Posts...)
		item := completed.Report
		report.PerAccount = append(report.PerAccount, item)
		report.APIRequests += item.APIRequests
		report.SourcePostsReturned += item.SourcePostsReturned
		report.SourcePostsScanned += item.SourcePostsScanned
		report.EligibleSelected += item.EligibleSelected
		if item.EscalatedToFull {
			report.EscalatedToFull++
		}
		if item.CoverageWindowExhausted {
			report.CoverageWindowExhausted++
			batch.CoverageWindowExhausted = append(batch.CoverageWindowExhausted, account.Key)
		}
	}
	sortSnapshotAccounts(batch.Accounts)
	sortSnapshotPosts(batch.Posts)
	if err := ValidateIncrementalBatch(batch, registry); err != nil {
		return IncrementalBatch{}, report, err
	}
	return batch, report, nil
}

// Match WriteSnapshotAtomic's exact representation, including its final newline.
func incrementalSnapshotFingerprint(snapshot Snapshot) (string, error) {
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	return sha256Hex(append(payload, '\n')), nil
}

func removeIncrementalCheckpoint(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove incremental checkpoint: %w", err)
	}
	return nil
}

func validateIncrementalCheckpointPaths(checkpointPath, snapshotPath string) error {
	fullCheckpoint := filepath.Join(filepath.Dir(snapshotPath), "x_fetch_checkpoint.json")
	if strings.TrimSpace(checkpointPath) == "" || pathsEqual(checkpointPath, snapshotPath) || pathsEqual(checkpointPath, fullCheckpoint) {
		return errors.New("incremental checkpoint path must differ from the snapshot and full-fetch checkpoint paths")
	}
	return nil
}
