package devdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type IncrementalRefreshOptions struct {
	Source         string
	Shard          string
	FetchCount     int
	SnapshotPath   string
	CheckpointPath string
	Now            func() time.Time
	Progress       func(string)
}

type IncrementalRefreshResult struct {
	Batch       IncrementalBatch
	FetchReport IncrementalFetchReport
	Snapshot    Snapshot
	Applied     bool
}

type IncrementalBatchApplier func(context.Context, IncrementalBatch) error

type incrementalRefreshFiles struct {
	writeCheckpoint  func(string, IncrementalCheckpoint) error
	writeSnapshot    func(string, string, Snapshot, SourceRegistry) error
	removeCheckpoint func(string) error
}

// RunIncrementalRefreshResumable requires the caller to hold the shared
// DevData mutation lock throughout fetching, applying, and file persistence.
// An old checkpoint is completed first, then the shard due at invocation time.
func RunIncrementalRefreshResumable(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, options IncrementalRefreshOptions, apply IncrementalBatchApplier) ([]IncrementalRefreshResult, error) {
	return runIncrementalRefreshResumable(ctx, client, registry, options, apply, incrementalRefreshFiles{
		writeCheckpoint:  WriteIncrementalCheckpointAtomic,
		writeSnapshot:    WriteIncrementalSnapshotIfUnchanged,
		removeCheckpoint: removeIncrementalCheckpoint,
	})
}

func runIncrementalRefreshResumable(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, options IncrementalRefreshOptions, apply IncrementalBatchApplier, files incrementalRefreshFiles) ([]IncrementalRefreshResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return nil, err
	}
	if client == nil || apply == nil {
		return nil, errors.New("incremental source and batch applier are required")
	}
	options.Source = strings.ToLower(strings.TrimSpace(options.Source))
	if options.Source != "rsshub" && options.Source != "x" {
		return nil, errors.New("incremental source must be rsshub or x")
	}
	if options.FetchCount < 5 || options.FetchCount > DefaultRSSHubFullFetchCount {
		return nil, errors.New("incremental fetch count must be between 5 and 60")
	}
	if strings.TrimSpace(options.SnapshotPath) == "" {
		return nil, errors.New("incremental snapshot path is required")
	}
	if options.CheckpointPath == "" {
		options.CheckpointPath = DefaultIncrementalCheckpointPath(options.SnapshotPath)
	}
	if err := validateIncrementalCheckpointPaths(options.CheckpointPath, options.SnapshotPath); err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	dueAt := checkpointNow(options.Now)
	dueShard, err := ParseIncrementalShard(options.Shard, dueAt)
	if err != nil {
		return nil, err
	}
	checkpoint, err := ReadIncrementalCheckpoint(options.CheckpointPath)
	hasCheckpoint := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if hasCheckpoint {
		if err := ValidateIncrementalCheckpoint(checkpoint, registry, options.Source, options.FetchCount); err != nil {
			return nil, err
		}
		if strings.ToLower(strings.TrimSpace(options.Shard)) != "auto" && checkpoint.Shard != dueShard {
			return nil, fmt.Errorf("pending incremental shard %d differs from requested shard %d; resume with --shard=auto or --shard=%d", checkpoint.Shard, dueShard, checkpoint.Shard)
		}
		emitProgress(options.Progress, "Resuming incremental checkpoint: shard=%d stage=%s completed=%d", checkpoint.Shard, checkpoint.Stage, len(checkpoint.Completed))
	}
	var results []IncrementalRefreshResult
	for {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		baseline, fingerprint, err := ReadIncrementalBaseline(options.SnapshotPath, registry)
		if err != nil {
			return results, err
		}
		if !hasCheckpoint {
			now := checkpointNow(options.Now)
			checkpoint = IncrementalCheckpoint{
				Version: IncrementalCheckpointVersion, Source: options.Source,
				RegistryFingerprint: RegistryFingerprint(registry), BaselineFingerprint: fingerprint,
				Shard: dueShard, FetchCount: options.FetchCount, Stage: IncrementalStageFetching,
				StartedAt: dueAt, UpdatedAt: now, Completed: make(map[string]IncrementalCompletedAccount),
			}
			if err := files.writeCheckpoint(options.CheckpointPath, checkpoint); err != nil {
				return results, err
			}
		}
		result, err := completeIncrementalCheckpoint(ctx, client, registry, baseline, fingerprint, &checkpoint, options, apply, files)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		sameSlot := checkpoint.StartedAt.Unix()/int64(IncrementalShardInterval/time.Second) == dueAt.Unix()/int64(IncrementalShardInterval/time.Second)
		if !hasCheckpoint || strings.ToLower(strings.TrimSpace(options.Shard)) != "auto" || (checkpoint.Shard == dueShard && sameSlot) {
			return results, nil
		}
		// Recovery must not consume a new slot, even when 24 hours have elapsed
		// and the same shard number is due again.
		emitProgress(options.Progress, "Recovered shard=%d; continuing with due shard=%d", checkpoint.Shard, dueShard)
		hasCheckpoint = false
	}
}

func completeIncrementalCheckpoint(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, fingerprint string, checkpoint *IncrementalCheckpoint, options IncrementalRefreshOptions, apply IncrementalBatchApplier, files incrementalRefreshFiles) (IncrementalRefreshResult, error) {
	result := IncrementalRefreshResult{}
	if fingerprint != checkpoint.BaselineFingerprint {
		// The previous process may have saved the final snapshot and crashed
		// before removing its synced checkpoint. Only that exact file is accepted.
		if checkpoint.Stage != IncrementalStageSynced {
			return result, ErrIncrementalSnapshotChanged
		}
		nextFingerprint, err := incrementalSnapshotFingerprint(*checkpoint.NextSnapshot)
		if err != nil || fingerprint != nextFingerprint {
			return result, ErrIncrementalSnapshotChanged
		}
	}
	if checkpoint.Stage == IncrementalStageFetching {
		if err := fetchPendingIncrementalAccounts(ctx, client, registry, baseline, checkpoint, options, files); err != nil {
			return result, err
		}
		batch, _, err := incrementalBatchFromCheckpoint(*checkpoint, registry)
		if err != nil {
			return result, err
		}
		next, err := MergeIncrementalSnapshot(baseline, batch, registry, checkpointNow(options.Now))
		if err != nil {
			return result, err
		}
		checkpoint.NextSnapshot = &next
		checkpoint.Stage = IncrementalStageReady
		checkpoint.UpdatedAt = checkpointNow(options.Now)
		if err := files.writeCheckpoint(options.CheckpointPath, *checkpoint); err != nil {
			return result, err
		}
	}
	batch, report, err := incrementalBatchFromCheckpoint(*checkpoint, registry)
	if err != nil {
		return result, err
	}
	if fingerprint == checkpoint.BaselineFingerprint {
		merged, err := MergeIncrementalSnapshot(baseline, batch, registry, checkpoint.NextSnapshot.FetchedAt)
		if err != nil {
			return result, err
		}
		mergedFingerprint, err := incrementalSnapshotFingerprint(merged)
		if err != nil {
			return result, err
		}
		nextFingerprint, err := incrementalSnapshotFingerprint(*checkpoint.NextSnapshot)
		if err != nil || nextFingerprint != mergedFingerprint {
			return result, errors.New("incremental checkpoint next snapshot does not match its saved batch")
		}
	}
	if checkpoint.Stage == IncrementalStageReady {
		// Detect standalone file changes before committing database work too.
		currentFingerprint, err := SnapshotFingerprint(options.SnapshotPath)
		if err != nil {
			return result, err
		}
		if currentFingerprint != checkpoint.BaselineFingerprint {
			return result, ErrIncrementalSnapshotChanged
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := apply(ctx, batch); err != nil {
			return result, fmt.Errorf("apply incremental shard %d; ready checkpoint preserved: %w", checkpoint.Shard, err)
		}
		result.Applied = true
		emitProgress(options.Progress, "Incremental database committed: shard=%d; snapshot persistence pending", checkpoint.Shard)
		checkpoint.Stage = IncrementalStageSynced
		checkpoint.UpdatedAt = checkpointNow(options.Now)
		if err := files.writeCheckpoint(options.CheckpointPath, *checkpoint); err != nil {
			return result, fmt.Errorf("database committed but synced checkpoint could not be saved; retry will safely replay the saved batch: %w", err)
		}
	}
	if fingerprint == checkpoint.BaselineFingerprint {
		if err := files.writeSnapshot(options.SnapshotPath, checkpoint.BaselineFingerprint, *checkpoint.NextSnapshot, registry); err != nil {
			return result, fmt.Errorf("database committed; synced checkpoint preserved for snapshot recovery: %w", err)
		}
	}
	if err := files.removeCheckpoint(options.CheckpointPath); err != nil {
		return result, err
	}
	result.Batch, result.FetchReport, result.Snapshot = batch, report, *checkpoint.NextSnapshot
	emitProgress(options.Progress, "Incremental checkpoint completed: shard=%d accounts=%d account_refresh_interval≈%dh", checkpoint.Shard, len(batch.Accounts), int(IncrementalShardInterval/time.Hour)*IncrementalShardCount)
	return result, nil
}

func fetchPendingIncrementalAccounts(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, checkpoint *IncrementalCheckpoint, options IncrementalRefreshOptions, files incrementalRefreshFiles) error {
	if checkpointNow(options.Now).Before(checkpoint.RetryNotBefore) {
		return fmt.Errorf("incremental rate-limit cooldown until %s; checkpoint preserved", checkpoint.RetryNotBefore.Format(time.RFC3339))
	}
	selected, err := SelectIncrementalShardAccounts(registry, checkpoint.Shard)
	if err != nil {
		return err
	}
	baselineIDs := baselineSourceIDsByAccount(baseline)
	for _, account := range selected {
		if _, ok := checkpoint.Completed[account.Key]; ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		data, report, err := fetchIncrementalAccount(ctx, client, account, baselineIDs[account.Key], checkpoint.FetchCount, options.Progress)
		if err != nil {
			var httpErr *RSSHubHTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == 429 && httpErr.RetryAfter > 0 {
				checkpoint.RetryNotBefore = checkpointNow(options.Now).Add(httpErr.RetryAfter)
				checkpoint.UpdatedAt = checkpointNow(options.Now)
				if saveErr := files.writeCheckpoint(options.CheckpointPath, *checkpoint); saveErr != nil {
					return fmt.Errorf("save incremental rate-limit cooldown: %w", saveErr)
				}
			}
			return fmt.Errorf("incremental shard %d incomplete: completed=%d/%d; checkpoint preserved: %w", checkpoint.Shard, len(checkpoint.Completed), len(selected), err)
		}
		checkpoint.Completed[account.Key] = IncrementalCompletedAccount{Data: data, Report: report}
		checkpoint.RetryNotBefore = time.Time{}
		checkpoint.UpdatedAt = checkpointNow(options.Now)
		if err := ValidateIncrementalCheckpoint(*checkpoint, registry, options.Source, options.FetchCount); err != nil {
			return err
		}
		if err := files.writeCheckpoint(options.CheckpointPath, *checkpoint); err != nil {
			return fmt.Errorf("save incremental checkpoint after %q: %w", account.Key, err)
		}
		emitProgress(options.Progress, "Incremental checkpoint saved: shard=%d account=%s completed=%d/%d", checkpoint.Shard, account.Key, len(checkpoint.Completed), len(selected))
	}
	return nil
}
