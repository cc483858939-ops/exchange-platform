package devdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultSourceBatchSize  = 5
	DefaultRSSHubBatchDelay = 60 * time.Second
	DefaultFetchMaxRetries  = 3
	MaxRSSHubRetryAfter     = 30 * time.Minute
	MaxRateLimitBackoff     = 15 * time.Minute
)

var defaultRateLimitBackoff = [...]time.Duration{
	60 * time.Second,
	120 * time.Second,
	240 * time.Second,
	480 * time.Second,
	900 * time.Second,
}

var ErrFetchIncomplete = errors.New("fetch incomplete")

// FetchIncompleteError reports a resumable, non-successful fetch. The
// checkpoint is intentionally left on disk for the next invocation.
type FetchIncompleteError struct {
	Completed int
	Total     int
}

func (e *FetchIncompleteError) Error() string {
	if e == nil {
		return ErrFetchIncomplete.Error()
	}
	return fmt.Sprintf("fetch incomplete: completed=%d/%d; checkpoint preserved", e.Completed, e.Total)
}

func (e *FetchIncompleteError) Unwrap() error { return ErrFetchIncomplete }

type FetchWaiter func(context.Context, time.Duration) error

// ResumableFetchOptions controls RSSHub batching. Wait and Now are injectable
// to keep retry and pacing tests deterministic without weakening production
// cancellation behavior.
type ResumableFetchOptions struct {
	BatchSize       int
	BatchDelay      time.Duration
	MaxRetries      int
	CheckpointPath  string
	SnapshotPath    string
	ResetCheckpoint bool
	Wait            FetchWaiter
	Sleeper         FetchWaiter
	Now             func() time.Time
	Clock           func() time.Time
	Progress        func(string)
}

func FetchRSSHubResumable(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, options ResumableFetchOptions) (Snapshot, FetchReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return Snapshot{}, FetchReport{}, err
	}
	if client == nil {
		return Snapshot{}, FetchReport{}, errors.New("source client is not initialized")
	}
	if err := validateResumableFetchOptions(&options); err != nil {
		return Snapshot{}, FetchReport{}, err
	}
	options.CheckpointPath = strings.TrimSpace(options.CheckpointPath)
	options.SnapshotPath = strings.TrimSpace(options.SnapshotPath)
	if options.CheckpointPath == "" {
		options.CheckpointPath = DefaultFetchCheckpointPath(".")
	}
	if options.SnapshotPath == "" {
		options.SnapshotPath = DefaultSnapshotPath(".")
	}
	if pathsEqual(options.CheckpointPath, options.SnapshotPath) {
		return Snapshot{}, FetchReport{}, errors.New("fetch checkpoint path must differ from snapshot path")
	}
	wait := options.Wait
	if wait == nil {
		wait = options.Sleeper
	}
	if wait == nil {
		wait = waitContext
	}
	now := options.Now
	if now == nil {
		now = options.Clock
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	fingerprint := RegistryFingerprint(registry)
	checkpoint, err := loadOrCreateFetchCheckpoint(options, registry, fingerprint, now)
	if err != nil {
		return Snapshot{}, FetchReport{}, err
	}
	if checkpoint.Completed == nil {
		checkpoint.Completed = make(map[string]FetchAccountData)
	}
	if checkpoint.Failures == nil {
		checkpoint.Failures = make(map[string]FetchFailure)
	}
	accounts := registry.EnabledAccounts()
	pending := pendingFetchAccounts(accounts, checkpoint)
	completedAtStart := len(checkpoint.Completed)
	if completedAtStart > 0 && len(pending) > 0 {
		emitProgress(options.Progress, "Resuming RSSHub checkpoint: completed=%d pending=%d", completedAtStart, len(pending))
	} else {
		emitProgress(options.Progress, "RSSHub fetch: completed=%d pending=%d", completedAtStart, len(pending))
	}

	runRequests := 0
	batchCount := batchCount(len(pending), options.BatchSize)
	for batchStart := 0; batchStart < len(pending); batchStart += options.BatchSize {
		batchEnd := batchStart + options.BatchSize
		if batchEnd > len(pending) {
			batchEnd = len(pending)
		}
		batchNumber := batchStart/options.BatchSize + 1
		emitProgress(options.Progress, "RSSHub fetch: completed=%d pending=%d batch=%d/%d", len(checkpoint.Completed), len(accounts)-len(checkpoint.Completed), batchNumber, batchCount)
		rateWaitUsed := false
		for _, account := range pending[batchStart:batchEnd] {
			rateRetries := 0
			for {
				beforeRequests, hasCounter := requestCountValue(client)
				data, fetchErr := FetchSnapshotAccount(ctx, client, account)
				afterRequests, _ := requestCountValue(client)
				runRequests += accountRequestDelta(beforeRequests, afterRequests, hasCounter, data, fetchErr)
				if fetchErr == nil {
					data.Report.RegistryKey = account.Key
					if hasCounter {
						data.Report.APIRequests = positiveRequestDelta(beforeRequests, afterRequests)
					}
					checkpoint.Completed[account.Key] = data
					delete(checkpoint.Failures, account.Key)
					checkpoint.UpdatedAt = checkpointNow(now)
					if err := WriteFetchCheckpointAtomic(options.CheckpointPath, checkpoint); err != nil {
						return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), fmt.Errorf("save fetch checkpoint after %q: %w", account.Key, err)
					}
					emitProgress(options.Progress, "Checkpoint saved: %s completed=%d pending=%d", options.CheckpointPath, len(checkpoint.Completed), len(accounts)-len(checkpoint.Completed))
					break
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), ctxErr
				}
				checkpointFailure(checkpoint, account.Key, fetchErr, now)
				if err := WriteFetchCheckpointAtomic(options.CheckpointPath, checkpoint); err != nil {
					return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), fmt.Errorf("save fetch checkpoint after %q failure: %w", account.Key, err)
				}
				emitProgress(options.Progress, "WARN: %s fetch failed: %v", account.Key, fetchErr)
				emitProgress(options.Progress, "Checkpoint saved: %s completed=%d pending=%d", options.CheckpointPath, len(checkpoint.Completed), len(accounts)-len(checkpoint.Completed))
				if !isRSSHubRateLimitError(fetchErr) {
					break
				}
				if rateRetries >= options.MaxRetries {
					return incompleteFetchResult(checkpoint, registry, runRequests, len(accounts))
				}
				delay := rateLimitRetryDelay(fetchErr, rateRetries)
				rateRetries++
				rateWaitUsed = true
				emitProgress(options.Progress, "WARN: RSSHub rate limited while fetching %s; retrying in %s", account.Key, delay)
				if err := waitForDelay(ctx, wait, delay); err != nil {
					return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), err
				}
			}
		}
		if batchEnd < len(pending) && !rateWaitUsed {
			emitProgress(options.Progress, "Waiting %s before next batch...", options.BatchDelay)
			if err := waitForDelay(ctx, wait, options.BatchDelay); err != nil {
				return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), err
			}
		}
	}

	if len(checkpoint.Completed) != len(accounts) {
		return incompleteFetchResult(checkpoint, registry, runRequests, len(accounts))
	}
	snapshot, err := AssembleSnapshotFromCheckpoint(checkpoint, registry, checkpointNow(now))
	if err != nil {
		return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), err
	}
	if err := WriteSnapshotAtomic(options.SnapshotPath, snapshot, registry); err != nil {
		return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), fmt.Errorf("write final X snapshot: %w", err)
	}
	emitProgress(options.Progress, "Snapshot written: %s", options.SnapshotPath)
	if err := RemoveFetchCheckpoint(options.CheckpointPath); err != nil {
		return Snapshot{}, reportFromCheckpointOrZero(checkpoint, registry, runRequests), err
	}
	emitProgress(options.Progress, "Checkpoint removed")
	report, reportErr := FetchReportFromCheckpoint(checkpoint, registry, runRequests)
	if reportErr != nil {
		return Snapshot{}, FetchReport{}, reportErr
	}
	return snapshot, report, nil
}

func PreflightRSSHubSources(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, options ResumableFetchOptions) ([]PreflightResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("source client is not initialized")
	}
	if err := validateResumableFetchOptions(&options); err != nil {
		return nil, err
	}
	wait := options.Wait
	if wait == nil {
		wait = options.Sleeper
	}
	if wait == nil {
		wait = waitContext
	}
	accounts := registry.EnabledAccounts()
	results := make([]PreflightResult, 0, len(accounts))
	for _, account := range accounts {
		results = append(results, PreflightResult{RegistryKey: account.Key, Handle: account.Handle, ProfileStatus: "pending"})
	}
	failed := false
	batchTotal := batchCount(len(accounts), options.BatchSize)
	for batchStart := 0; batchStart < len(accounts); batchStart += options.BatchSize {
		batchEnd := batchStart + options.BatchSize
		if batchEnd > len(accounts) {
			batchEnd = len(accounts)
		}
		rateWaitUsed := false
		emitProgress(options.Progress, "RSSHub preflight: batch=%d/%d", batchStart/options.BatchSize+1, batchTotal)
		for index := batchStart; index < batchEnd; index++ {
			account := accounts[index]
			rateRetries := 0
			for {
				users, lookupErr := client.LookupUsers(ctx, []string{account.Handle})
				if lookupErr == nil {
					results[index].Error = ""
					user, exists := users[strings.ToLower(account.Handle)]
					if !exists {
						results[index].ProfileStatus = "missing"
						results[index].Error = "source user was not returned"
						failed = true
					} else if err := fillPreflightResult(&results[index], account, user); err != nil {
						failed = true
					}
					break
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return results, ctxErr
				}
				results[index].ProfileStatus = "missing"
				results[index].Error = lookupErr.Error()
				if !isRSSHubRateLimitError(lookupErr) {
					failed = true
					break
				}
				results[index].ProfileStatus = "rate_limited"
				if rateRetries >= options.MaxRetries {
					failed = true
					markPreflightNotAttempted(results, index+1, fmt.Sprintf("source preflight stopped after rate limit: %v", lookupErr))
					return results, ErrPreflightFailed
				}
				delay := rateLimitRetryDelay(lookupErr, rateRetries)
				rateRetries++
				rateWaitUsed = true
				emitProgress(options.Progress, "WARN: RSSHub rate limited while preflighting %s; retrying in %s", account.Key, delay)
				if err := waitForDelay(ctx, wait, delay); err != nil {
					return results, err
				}
			}
		}
		if batchEnd < len(accounts) && !rateWaitUsed {
			emitProgress(options.Progress, "Waiting %s before next preflight batch...", options.BatchDelay)
			if err := waitForDelay(ctx, wait, options.BatchDelay); err != nil {
				return results, err
			}
		}
	}
	if failed {
		return results, ErrPreflightFailed
	}
	return results, nil
}

func validateResumableFetchOptions(options *ResumableFetchOptions) error {
	if options.BatchSize == 0 {
		options.BatchSize = DefaultSourceBatchSize
	}
	if options.BatchSize < 1 {
		return errors.New("batch-size must be at least 1")
	}
	if options.BatchDelay < 0 {
		return errors.New("batch-delay must be non-negative")
	}
	if options.MaxRetries < 0 {
		return errors.New("max-retries must be non-negative")
	}
	return nil
}

func pathsEqual(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbsolute, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return strings.EqualFold(leftAbsolute, rightAbsolute)
}

func loadOrCreateFetchCheckpoint(options ResumableFetchOptions, registry SourceRegistry, fingerprint string, now func() time.Time) (FetchCheckpoint, error) {
	if options.ResetCheckpoint {
		if err := RemoveFetchCheckpoint(options.CheckpointPath); err != nil {
			return FetchCheckpoint{}, err
		}
		emitProgress(options.Progress, "Fetch checkpoint reset: %s", options.CheckpointPath)
	}
	checkpoint, err := ReadFetchCheckpoint(options.CheckpointPath)
	if err == nil {
		if err := ValidateFetchCheckpoint(checkpoint, registry, FetchCheckpointSource); err != nil {
			return FetchCheckpoint{}, err
		}
		return checkpoint, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return FetchCheckpoint{}, err
	}
	createdAt := checkpointNow(now)
	checkpoint = FetchCheckpoint{
		Version:             FetchCheckpointVersion,
		Source:              FetchCheckpointSource,
		RegistryFingerprint: fingerprint,
		StartedAt:           createdAt,
		UpdatedAt:           createdAt,
		Completed:           make(map[string]FetchAccountData),
		Failures:            make(map[string]FetchFailure),
	}
	if err := WriteFetchCheckpointAtomic(options.CheckpointPath, checkpoint); err != nil {
		return FetchCheckpoint{}, err
	}
	emitProgress(options.Progress, "Checkpoint created: %s", options.CheckpointPath)
	return checkpoint, nil
}

func pendingFetchAccounts(accounts []SourceAccount, checkpoint FetchCheckpoint) []SourceAccount {
	pending := make([]SourceAccount, 0, len(accounts))
	for _, account := range accounts {
		if _, completed := checkpoint.Completed[account.Key]; !completed {
			pending = append(pending, account)
		}
	}
	return pending
}

func checkpointFailure(checkpoint FetchCheckpoint, key string, err error, now func() time.Time) {
	if checkpoint.Failures == nil {
		checkpoint.Failures = make(map[string]FetchFailure)
	}
	failure := checkpoint.Failures[key]
	failure.Attempts++
	failure.LastError = err.Error()
	failure.LastFailedAt = checkpointNow(now)
	checkpoint.Failures[key] = failure
	checkpoint.UpdatedAt = failure.LastFailedAt
}

func incompleteFetchResult(checkpoint FetchCheckpoint, registry SourceRegistry, runRequests, total int) (Snapshot, FetchReport, error) {
	report, reportErr := FetchReportFromCheckpoint(checkpoint, registry, runRequests)
	if reportErr != nil {
		return Snapshot{}, FetchReport{}, reportErr
	}
	return Snapshot{}, report, &FetchIncompleteError{Completed: len(checkpoint.Completed), Total: total}
}

func reportFromCheckpointOrZero(checkpoint FetchCheckpoint, registry SourceRegistry, runRequests int) FetchReport {
	report, err := FetchReportFromCheckpoint(checkpoint, registry, runRequests)
	if err != nil {
		return FetchReport{APIRequests: runRequests}
	}
	return report
}

func requestCountValue(client SnapshotSourceClient) (int, bool) {
	counter, ok := client.(sourceRequestCounter)
	if !ok {
		return 0, false
	}
	return counter.RequestCount(), true
}

func positiveRequestDelta(before, after int) int {
	if after <= before {
		return 0
	}
	return after - before
}

func accountRequestDelta(before, after int, hasCounter bool, data FetchAccountData, fetchErr error) int {
	if hasCounter {
		return positiveRequestDelta(before, after)
	}
	if data.Report.APIRequests > 0 {
		return data.Report.APIRequests
	}
	if fetchErr != nil {
		return 1
	}
	return 0
}

func rateLimitRetryDelay(err error, retryIndex int) time.Duration {
	var httpErr *RSSHubHTTPError
	if errors.As(err, &httpErr) && httpErr != nil && httpErr.RetryAfter > 0 {
		if httpErr.RetryAfter > MaxRSSHubRetryAfter {
			return MaxRSSHubRetryAfter
		}
		return httpErr.RetryAfter
	}
	if retryIndex < 0 {
		retryIndex = 0
	}
	if retryIndex >= len(defaultRateLimitBackoff) {
		return MaxRateLimitBackoff
	}
	delay := defaultRateLimitBackoff[retryIndex]
	if delay > MaxRateLimitBackoff {
		return MaxRateLimitBackoff
	}
	return delay
}

func isRSSHubRateLimitError(err error) bool {
	var httpErr *RSSHubHTTPError
	return errors.As(err, &httpErr) && httpErr != nil && httpErr.StatusCode == 429
}

func IsRSSHubRateLimitError(err error) bool { return isRSSHubRateLimitError(err) }

func waitContext(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitForDelay(ctx context.Context, wait FetchWaiter, delay time.Duration) error {
	if delay <= 0 {
		if ctx == nil {
			return nil
		}
		return ctx.Err()
	}
	return wait(ctx, delay)
}

func batchCount(total, size int) int {
	if total == 0 {
		return 0
	}
	return (total + size - 1) / size
}

func checkpointNow(now func() time.Time) time.Time {
	value := now()
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC()
}

func emitProgress(progress func(string), format string, args ...interface{}) {
	if progress != nil {
		progress(fmt.Sprintf(format, args...))
	}
}

func markPreflightNotAttempted(results []PreflightResult, start int, message string) {
	for index := start; index < len(results); index++ {
		if results[index].ProfileStatus == "pending" {
			results[index].ProfileStatus = "not_attempted"
			results[index].Error = message
		}
	}
}
