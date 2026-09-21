package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const incrementalMaximumFetchCount = DefaultRSSHubFullFetchCount

var ErrIncrementalBaselineRequired = errors.New("incremental refresh requires a valid full baseline snapshot; run a full refresh first")

type IncrementalBatch struct {
	FetchedAt               time.Time
	Shard                   int
	Accounts                []SnapshotAccount
	Posts                   []SnapshotPost
	CoverageWindowExhausted []string
}

type IncrementalAccountReport struct {
	RegistryKey             string
	FetchCount              int
	SourcePostsReturned     int
	SourcePostsScanned      int
	EligibleSelected        int
	APIRequests             int
	EscalatedToFull         bool
	CoverageWindowExhausted bool
}

type IncrementalFetchReport struct {
	Shard                   int
	FetchCount              int
	Accounts                int
	APIRequests             int
	SourcePostsScanned      int
	SourcePostsReturned     int
	EligibleSelected        int
	EscalatedToFull         int
	CoverageWindowExhausted int
	PerAccount              []IncrementalAccountReport
}

type IncrementalFetchOptions struct {
	Shard      int
	FetchCount int
	FetchedAt  time.Time
	Now        func() time.Time
	Progress   func(string)
}

// FetchIncrementalBatch fetches one balanced shard using the configured
// bounded source window and returns no partial batch on any account failure.
func FetchIncrementalBatch(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, shard, fetchCount int, fetchedAt time.Time) (IncrementalBatch, IncrementalFetchReport, error) {
	return FetchIncrementalBatchWithOptions(ctx, client, registry, baseline, IncrementalFetchOptions{
		Shard: shard, FetchCount: fetchCount, FetchedAt: fetchedAt,
	})
}

func FetchIncrementalBatchWithOptions(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, baseline Snapshot, options IncrementalFetchOptions) (IncrementalBatch, IncrementalFetchReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateRegistry(registry); err != nil {
		return IncrementalBatch{}, IncrementalFetchReport{}, err
	}
	if client == nil {
		return IncrementalBatch{}, IncrementalFetchReport{}, errors.New("source client is not initialized")
	}
	if err := ValidateSnapshot(baseline, registry); err != nil {
		return IncrementalBatch{}, IncrementalFetchReport{}, fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if options.FetchCount == 0 {
		options.FetchCount = DefaultRSSHubIncrementalFetchCount
	}
	if options.FetchCount < 5 || options.FetchCount > incrementalMaximumFetchCount {
		return IncrementalBatch{}, IncrementalFetchReport{}, fmt.Errorf("fetch-count must be between 5 and %d", incrementalMaximumFetchCount)
	}
	selected, err := SelectIncrementalShardAccounts(registry, options.Shard)
	if err != nil {
		return IncrementalBatch{}, IncrementalFetchReport{}, err
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	fetchedAt := options.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = checkpointNow(now)
	}
	baselineIDs := baselineSourceIDsByAccount(baseline)
	batch := IncrementalBatch{
		FetchedAt: fetchedAt.UTC(),
		Shard:     options.Shard,
		Accounts:  make([]SnapshotAccount, 0, len(selected)),
		Posts:     make([]SnapshotPost, 0, len(selected)*DefaultRSSHubIncrementalFetchCount),
	}
	report := IncrementalFetchReport{
		Shard:      options.Shard,
		FetchCount: options.FetchCount,
		Accounts:   len(selected),
		PerAccount: make([]IncrementalAccountReport, 0, len(selected)),
	}
	for _, configured := range selected {
		data, accountReport, fetchErr := fetchIncrementalAccount(ctx, client, configured, baselineIDs[configured.Key], options.FetchCount, options.Progress)
		if fetchErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return IncrementalBatch{}, report, ctxErr
			}
			return IncrementalBatch{}, report, fetchErr
		}
		if accountReport.CoverageWindowExhausted {
			batch.CoverageWindowExhausted = append(batch.CoverageWindowExhausted, configured.Key)
		}

		batch.Accounts = append(batch.Accounts, data.Account)
		batch.Posts = append(batch.Posts, data.Posts...)
		report.PerAccount = append(report.PerAccount, accountReport)
		report.APIRequests += accountReport.APIRequests
		report.SourcePostsScanned += accountReport.SourcePostsScanned
		report.SourcePostsReturned += accountReport.SourcePostsReturned
		report.EligibleSelected += accountReport.EligibleSelected
		if accountReport.EscalatedToFull {
			report.EscalatedToFull++
		}
		if accountReport.CoverageWindowExhausted {
			report.CoverageWindowExhausted++
		}
		emitProgress(options.Progress, "Incremental fetched %s: source=%d eligible=%d window=%d", configured.Key, accountReport.SourcePostsReturned, accountReport.EligibleSelected, accountReport.FetchCount)
	}
	sortSnapshotAccounts(batch.Accounts)
	sortSnapshotPosts(batch.Posts)
	return batch, report, nil
}

// fetchIncrementalAccount fetches one account using the same bounded-window
// and coverage-escalation rules as the shard refresh. Targeted refresh reuses
// this helper so the two commands cannot drift in their source behavior.
func fetchIncrementalAccount(ctx context.Context, client SnapshotSourceClient, configured SourceAccount, baselineIDs map[string]struct{}, fetchCount int, progress func(string)) (FetchAccountData, IncrementalAccountReport, error) {
	beforeRequests, hasCounter := requestCountValue(client)
	data, rawPosts, fetchErr := FetchSnapshotAccountWithFetchCount(ctx, client, configured, fetchCount)
	afterRequests, _ := requestCountValue(client)
	if fetchErr != nil {
		emitIncrementalRateLimit(progress, configured.Key, fetchErr)
		return FetchAccountData{}, IncrementalAccountReport{}, fmt.Errorf("incremental fetch failed for %q: %w", configured.Key, fetchErr)
	}
	accountReport := IncrementalAccountReport{
		RegistryKey:         configured.Key,
		FetchCount:          fetchCount,
		SourcePostsReturned: len(rawPosts),
		SourcePostsScanned:  data.Report.SourcePostsScanned,
		EligibleSelected:    data.Report.EligibleSelected,
		APIRequests:         accountRequestDelta(beforeRequests, afterRequests, hasCounter, data, nil),
	}
	if !hasCounter && accountReport.APIRequests == 0 {
		accountReport.APIRequests = data.Report.APIRequests
	}

	if fetchCount < incrementalMaximumFetchCount && len(rawPosts) == fetchCount && !hasSourcePostOverlap(rawPosts, baselineIDs) {
		emitProgress(progress, "Incremental coverage saturated for %s; escalating fetch window to %d", configured.Key, incrementalMaximumFetchCount)
		beforeFallback, fallbackHasCounter := requestCountValue(client)
		data, rawPosts, fetchErr = FetchSnapshotAccountWithFetchCount(ctx, client, configured, incrementalMaximumFetchCount)
		afterFallback, _ := requestCountValue(client)
		accountReport.FetchCount = incrementalMaximumFetchCount
		accountReport.SourcePostsReturned = len(rawPosts)
		accountReport.SourcePostsScanned = data.Report.SourcePostsScanned
		accountReport.EligibleSelected = data.Report.EligibleSelected
		accountReport.EscalatedToFull = true
		if fallbackHasCounter {
			accountReport.APIRequests += positiveRequestDelta(beforeFallback, afterFallback)
		} else {
			accountReport.APIRequests += data.Report.APIRequests
		}
		if fetchErr != nil {
			emitIncrementalRateLimit(progress, configured.Key, fetchErr)
			return FetchAccountData{}, accountReport, fmt.Errorf("incremental coverage fallback failed for %q: %w", configured.Key, fetchErr)
		}
	}
	if accountReport.FetchCount == incrementalMaximumFetchCount && accountReport.SourcePostsReturned == incrementalMaximumFetchCount && !hasSourcePostOverlap(rawPosts, baselineIDs) {
		accountReport.CoverageWindowExhausted = true
		emitProgress(progress, "WARN: incremental coverage_window_exhausted=true account=%s window=%d; history is not known complete", configured.Key, incrementalMaximumFetchCount)
	}
	return data, accountReport, nil
}

func baselineSourceIDsByAccount(snapshot Snapshot) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		result[account.RegistryKey] = make(map[string]struct{})
	}
	for _, post := range snapshot.Posts {
		if _, ok := result[post.RegistryKey]; !ok {
			result[post.RegistryKey] = make(map[string]struct{})
		}
		result[post.RegistryKey][strings.TrimSpace(post.SourcePostID)] = struct{}{}
	}
	return result
}

func hasSourcePostOverlap(posts []XPost, baselineIDs map[string]struct{}) bool {
	for _, post := range posts {
		if _, ok := baselineIDs[strings.TrimSpace(post.ID)]; ok && strings.TrimSpace(post.ID) != "" {
			return true
		}
	}
	return false
}

func emitIncrementalRateLimit(progress func(string), accountKey string, err error) {
	if !isRSSHubRateLimitError(err) {
		return
	}
	var httpErr *RSSHubHTTPError
	if errors.As(err, &httpErr) && httpErr != nil && httpErr.RetryAfter > 0 {
		emitProgress(progress, "WARN: RSSHub rate limited on %s; stopping this shard; retry after %s", accountKey, httpErr.RetryAfter.Round(time.Second))
		return
	}
	emitProgress(progress, "WARN: RSSHub rate limited on %s; stopping this shard", accountKey)
}
