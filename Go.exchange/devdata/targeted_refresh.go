package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TargetedRefreshOptions controls a single-account source refresh. The
// default fetch count intentionally matches the bounded incremental window.
type TargetedRefreshOptions struct {
	RegistryKey string
	FetchCount  int
	FetchedAt   time.Time
	Progress    func(string)
}

// TargetedRefreshBatch is the one-account desired-state fragment produced by
// FetchTargetedAccount. It is never a complete snapshot and must be merged
// into a valid baseline before it is written or synchronized.
type TargetedRefreshBatch struct {
	RegistryKey             string
	FetchedAt               time.Time
	Accounts                []SnapshotAccount
	Posts                   []SnapshotPost
	CoverageWindowExhausted bool
}

// FetchTargetedAccount resolves and fetches exactly one enabled registry
// account. The baseline is validated before any source request is made.
func FetchTargetedAccount(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, options TargetedRefreshOptions) (TargetedRefreshBatch, IncrementalAccountReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, err
	}
	configured, err := resolveTargetedAccount(registry, options.RegistryKey)
	if err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, err
	}
	if err := ValidateSnapshot(baseline, registry); err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	return fetchTargetedAccount(ctx, client, registry, baseline, configured, options)
}

// FetchTargetedReplacementAccount fetches only the enabled new account after
// validating the transition-aware baseline. It never resolves or requests the
// retired key or any unrelated account.
func FetchTargetedReplacementAccount(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, replacement RegistryReplacement, options TargetedRefreshOptions) (TargetedRefreshBatch, IncrementalAccountReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, err
	}
	replacement = normalizedReplacement(replacement)
	if err := ValidateReplacementBaseline(baseline, registry, replacement); err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	configured, err := resolveTargetedAccount(registry, replacement.NewKey)
	if err != nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, err
	}
	options.RegistryKey = configured.Key
	return fetchTargetedAccount(ctx, client, registry, baseline, configured, options)
}

func fetchTargetedAccount(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, configured SourceAccount, options TargetedRefreshOptions) (TargetedRefreshBatch, IncrementalAccountReport, error) {
	if client == nil {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, errors.New("source client is not initialized")
	}
	fetchCount := options.FetchCount
	if fetchCount == 0 {
		fetchCount = DefaultRSSHubIncrementalFetchCount
	}
	if fetchCount < 5 || fetchCount > incrementalMaximumFetchCount {
		return TargetedRefreshBatch{}, IncrementalAccountReport{}, fmt.Errorf("fetch-count must be between 5 and %d", incrementalMaximumFetchCount)
	}
	fetchedAt := options.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	baselineIDs := baselineSourceIDsByAccount(baseline)
	data, report, err := fetchIncrementalAccount(ctx, client, configured, baselineIDs[configured.Key], fetchCount, options.Progress)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TargetedRefreshBatch{}, report, ctxErr
		}
		return TargetedRefreshBatch{}, report, err
	}
	batch := TargetedRefreshBatch{
		RegistryKey:             configured.Key,
		FetchedAt:               fetchedAt.UTC(),
		Accounts:                []SnapshotAccount{data.Account},
		Posts:                   data.Posts,
		CoverageWindowExhausted: report.CoverageWindowExhausted,
	}
	if err := ValidateTargetedRefreshBatch(batch, registry); err != nil {
		return TargetedRefreshBatch{}, report, err
	}
	emitProgress(options.Progress, "Targeted fetched %s: source=%d eligible=%d window=%d", configured.Key, report.SourcePostsReturned, report.EligibleSelected, report.FetchCount)
	return batch, report, nil
}

// ValidateTargetedRefreshBatch validates the one-account fragment against a
// temporary registry while preserving the complete-snapshot validation rules.
func ValidateTargetedRefreshBatch(batch TargetedRefreshBatch, registry SourceRegistry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	configured, err := resolveTargetedAccount(registry, batch.RegistryKey)
	if err != nil {
		return err
	}
	if batch.FetchedAt.IsZero() {
		return errors.New("targeted refresh batch fetched_at is required")
	}
	if len(batch.Accounts) != 1 {
		return fmt.Errorf("targeted refresh batch must contain exactly one account, got %d", len(batch.Accounts))
	}
	if batch.Accounts[0].RegistryKey != configured.Key {
		return fmt.Errorf("targeted refresh batch account %q does not match target %q", batch.Accounts[0].RegistryKey, configured.Key)
	}
	for _, post := range batch.Posts {
		if post.RegistryKey != configured.Key {
			return fmt.Errorf("targeted refresh batch contains post for account %q", post.RegistryKey)
		}
	}
	subset := SourceRegistry{
		Version:         registry.Version,
		DefaultMaxPosts: registry.DefaultMaxPosts,
		Accounts:        []SourceAccount{configured},
	}
	fragment := Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: batch.FetchedAt.UTC(),
		Accounts:  append([]SnapshotAccount(nil), batch.Accounts...),
		Posts:     cloneSnapshotPosts(batch.Posts),
	}
	if err := ValidateSnapshot(fragment, subset); err != nil {
		return fmt.Errorf("invalid targeted refresh batch: %w", err)
	}
	return nil
}

// ResolveTargetedAccount validates the registry key without making a source
// request. The CLI uses it to fail early for unknown or disabled accounts.
func ResolveTargetedAccount(registry SourceRegistry, key string) (SourceAccount, error) {
	if err := ValidateRegistry(registry); err != nil {
		return SourceAccount{}, err
	}
	return resolveTargetedAccount(registry, key)
}

// MergeTargetedAccountSnapshot replaces only the selected account in a
// complete rolling snapshot. Posts for the selected account use the same
// source-ID replacement, sorting, and max_posts trimming rules as incremental
// refresh; all other account data is retained.
func MergeTargetedAccountSnapshot(baseline Snapshot, batch TargetedRefreshBatch, registry SourceRegistry, now time.Time) (Snapshot, error) {
	if err := ValidateSnapshot(baseline, registry); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if err := ValidateTargetedRefreshBatch(batch, registry); err != nil {
		return Snapshot{}, err
	}
	return mergeSelectedSnapshot(
		baseline,
		batch.Accounts,
		batch.Posts,
		registry,
		map[string]struct{}{batch.RegistryKey: {}},
		batch.FetchedAt,
		now,
		"targeted refresh",
	)
}

func resolveTargetedAccount(registry SourceRegistry, rawKey string) (SourceAccount, error) {
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return SourceAccount{}, errors.New("registry key is required")
	}
	account, exists := registry.AccountByKey(key)
	if !exists {
		return SourceAccount{}, fmt.Errorf("unknown registry key %q", key)
	}
	if !account.Enabled {
		return SourceAccount{}, fmt.Errorf("registry account %q is disabled", key)
	}
	return account, nil
}
