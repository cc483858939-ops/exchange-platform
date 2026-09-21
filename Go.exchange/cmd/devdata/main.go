package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/devdata"
	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: go run ./cmd/devdata <fetch|refresh|refresh-incremental|refresh-account|rebuild|verify|verify-avatars> [flags]")
	}
	baseDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	switch args[0] {
	case "fetch":
		options, err := parseCommandFlags("fetch", args[1:], stderr, false)
		if err != nil {
			return err
		}
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		client, err := newLiveSource(options.source)
		if err != nil {
			return err
		}
		_, report, err := fetchSnapshotForCommand(context.Background(), client, registry, options, baseDir, stdout)
		if err != nil {
			return err
		}
		if options.source == "rsshub" {
			writeFetchReportSummary(stdout, report)
		} else {
			writeFetchReport(stdout, report, options.snapshotPath(baseDir))
		}
		return nil
	case "refresh":
		options, err := parseCommandFlags("refresh", args[1:], stderr, true)
		if err != nil {
			return err
		}
		db, err := initDatabase()
		if err != nil {
			return err
		}
		lock, err := devdata.AcquireDevDataMutationLock(context.Background(), db)
		if err != nil {
			return err
		}
		defer func() {
			if releaseErr := lock.Release(context.Background()); releaseErr != nil {
				fmt.Fprintf(stderr, "WARN: release DevData mutation lock: %v\n", releaseErr)
			}
		}()
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		client, err := newLiveSource(options.source)
		if err != nil {
			return err
		}
		snapshot, report, err := fetchSnapshotForCommand(context.Background(), client, registry, options, baseDir, stdout)
		if err != nil {
			return err
		}
		if options.source == "rsshub" {
			writeFetchReportSummary(stdout, report)
		} else {
			writeFetchReport(stdout, report, options.snapshotPath(baseDir))
		}
		redisClient := bestEffortRedis(stderr)
		if redisClient != nil {
			defer redisClient.Close()
		}
		var avatarStore devdata.AvatarObjectStore
		storageClient, storageErr := config.NewStorageClient()
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: mirror storage unavailable; avatar, cover, and PostMedia localization will be skipped")
		} else {
			avatarStore, err = devdata.NewMinioAvatarObjectStore(storageClient)
			if err != nil {
				fmt.Fprintln(stderr, "WARN: mirror storage adapter unavailable; avatar, cover, and PostMedia localization will be skipped")
			}
		}
		var avatarFetcher devdata.AvatarFetcher
		if avatarStore != nil {
			avatarFetcher = devdata.NewAvatarDownloader()
		}
		resolutions, avatarReport, err := devdata.PrepareAvatarMirrors(context.Background(), registry, snapshot, avatarFetcher, avatarStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Avatars: attempted=%d uploaded=%d reused=%d failed=%d\n", avatarReport.Attempted, avatarReport.Uploaded, avatarReport.Reused, avatarReport.Failed)
		var coverFetcher devdata.CoverFetcher
		if avatarStore != nil {
			coverFetcher = devdata.NewCoverDownloader()
		}
		coverResolutions, coverReport, err := devdata.PrepareCoverMirrors(context.Background(), registry, snapshot, coverFetcher, avatarStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Covers: attempted=%d uploaded=%d reused=%d cleared=%d failed=%d\n", coverReport.Attempted, coverReport.Uploaded, coverReport.Reused, coverReport.Cleared, coverReport.Failed)
		var postMediaFetcher devdata.PostMediaFetcher
		if avatarStore != nil {
			postMediaFetcher = devdata.NewPostMediaDownloader()
		}
		postMediaResolutions, postMediaReport, err := devdata.PreparePostMediaMirrors(context.Background(), registry, snapshot, postMediaFetcher, avatarStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Post media: posts=%d attempted=%d uploaded=%d reused=%d failed=%d\n", postMediaReport.PostsWithMedia, postMediaReport.Attempted, postMediaReport.Uploaded, postMediaReport.Reused, postMediaReport.Failed)
		if err := syncAndVerifyWithDB(stdout, registry, snapshot, db, redisClient, devdata.SyncOptions{
			AvatarResolutions:                    resolutions,
			CoverResolutions:                     coverResolutions,
			PostMediaResolutions:                 postMediaResolutions,
			PreserveExistingAvatarWhenUnresolved: true,
			PreserveExistingCoverWhenUnresolved:  true,
		}); err != nil {
			return err
		}
		writeMediaLocalizationWarning(stderr, avatarReport.Failed, postMediaReport.Failed)
		writeCoverLocalizationWarning(stderr, coverReport.Failed)
		return nil
	case "refresh-incremental":
		options, err := parseCommandFlags("refresh-incremental", args[1:], stderr, false)
		if err != nil {
			return err
		}
		return runIncrementalRefresh(context.Background(), baseDir, options, stdout, stderr)
	case "refresh-account":
		options, err := parseCommandFlags("refresh-account", args[1:], stderr, false)
		if err != nil {
			return err
		}
		return runTargetedRefresh(context.Background(), baseDir, options, stdout, stderr)
	case "rebuild":
		options, err := parseCommandFlags("rebuild", args[1:], stderr, true)
		if err != nil {
			return err
		}
		db, err := initDatabase()
		if err != nil {
			return err
		}
		lock, err := devdata.AcquireDevDataMutationLock(context.Background(), db)
		if err != nil {
			return err
		}
		defer func() {
			if releaseErr := lock.Release(context.Background()); releaseErr != nil {
				fmt.Fprintf(stderr, "WARN: release DevData mutation lock: %v\n", releaseErr)
			}
		}()
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		snapshot, err := devdata.ReadSnapshot(options.snapshotPath(baseDir), registry)
		if err != nil {
			return err
		}
		var mirrorStore devdata.AvatarObjectStore
		storageClient, storageErr := config.NewStorageClient()
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: mirror storage unavailable; existing avatars, covers, and PostMedia rows will be preserved for unresolved images")
		} else {
			mirrorStore, storageErr = devdata.NewMinioAvatarObjectStore(storageClient)
			if storageErr != nil {
				fmt.Fprintln(stderr, "WARN: mirror storage adapter unavailable; existing avatars, covers, and PostMedia rows will be preserved for unresolved images")
			}
		}
		var avatarFetcher devdata.AvatarFetcher
		var coverFetcher devdata.CoverFetcher
		var postMediaFetcher devdata.PostMediaFetcher
		if mirrorStore != nil {
			avatarFetcher = devdata.NewAvatarDownloader()
			coverFetcher = devdata.NewCoverDownloader()
			postMediaFetcher = devdata.NewPostMediaDownloader()
		}
		avatarResolutions, avatarReport, err := devdata.PrepareAvatarMirrors(context.Background(), registry, snapshot, avatarFetcher, mirrorStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Avatars: attempted=%d uploaded=%d reused=%d failed=%d\n", avatarReport.Attempted, avatarReport.Uploaded, avatarReport.Reused, avatarReport.Failed)
		coverResolutions, coverReport, err := devdata.PrepareCoverMirrors(context.Background(), registry, snapshot, coverFetcher, mirrorStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Covers: attempted=%d uploaded=%d reused=%d cleared=%d failed=%d\n", coverReport.Attempted, coverReport.Uploaded, coverReport.Reused, coverReport.Cleared, coverReport.Failed)
		postMediaResolutions, postMediaReport, err := devdata.PreparePostMediaMirrors(context.Background(), registry, snapshot, postMediaFetcher, mirrorStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Post media: posts=%d attempted=%d uploaded=%d reused=%d failed=%d\n", postMediaReport.PostsWithMedia, postMediaReport.Attempted, postMediaReport.Uploaded, postMediaReport.Reused, postMediaReport.Failed)
		redisClient := bestEffortRedis(stderr)
		if redisClient != nil {
			defer redisClient.Close()
		}
		if err := syncAndVerifyWithDB(stdout, registry, snapshot, db, redisClient, devdata.SyncOptions{
			AvatarResolutions:                    avatarResolutions,
			CoverResolutions:                     coverResolutions,
			PostMediaResolutions:                 postMediaResolutions,
			PreserveExistingAvatarWhenUnresolved: true,
			PreserveExistingCoverWhenUnresolved:  true,
		}); err != nil {
			return err
		}
		writeMediaLocalizationWarning(stderr, avatarReport.Failed, postMediaReport.Failed)
		writeCoverLocalizationWarning(stderr, coverReport.Failed)
		return nil
	case "verify":
		options, err := parseCommandFlags("verify", args[1:], stderr, false)
		if err != nil {
			return err
		}
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		db, err := initDatabase()
		if err != nil {
			return err
		}
		verification, err := devdata.VerifyCoreWithOptions(context.Background(), db, registry, time.Now().UTC(), devdata.VerificationOptions{Mode: devdata.VerificationModeCuratedV1Live})
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, devdata.FormatCoreVerification(verification))
		return err
	case "verify-avatars":
		options, err := parseCommandFlags("verify-avatars", args[1:], stderr, false)
		if err != nil {
			return err
		}
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		db, err := initDatabase()
		if err != nil {
			return err
		}
		storageClient, storageErr := config.NewStorageClient()
		var avatarStore devdata.AvatarObjectStore
		if storageErr == nil {
			avatarStore, err = devdata.NewMinioAvatarObjectStore(storageClient)
			if err != nil {
				storageErr = errors.New("avatar storage adapter is unavailable")
			}
		}
		verification, verifyErr := devdata.VerifyAvatars(context.Background(), db, registry, avatarStore)
		fmt.Fprintf(stdout, "Avatar verification: enabled=%d local_urls=%d objects_present=%d invalid=%d\n", verification.Enabled, verification.LocalURLs, verification.ObjectsPresent, verification.Invalid)
		if verifyErr != nil {
			return verifyErr
		}
		if storageErr != nil {
			return errors.New("avatar storage is unavailable")
		}
		return nil
	default:
		return fmt.Errorf("unknown devdata command %q", args[0])
	}
}

type commandOptions struct {
	source           string
	profile          string
	allowDestructive bool
	key              string
	shard            string
	fetchCount       int
	registry         string
	snapshot         string
	checkpoint       string
	resetCheckpoint  bool
	batchSize        int
	batchDelay       time.Duration
}

func (o commandOptions) registryPath(baseDir string) string {
	if strings.TrimSpace(o.registry) == "" {
		return devdata.DefaultRegistryPath(baseDir)
	}
	return o.registry
}

func (o commandOptions) snapshotPath(baseDir string) string {
	if strings.TrimSpace(o.snapshot) == "" {
		return devdata.DefaultSnapshotPath(baseDir)
	}
	return o.snapshot
}

func (o commandOptions) checkpointPath(baseDir string) string {
	if strings.TrimSpace(o.checkpoint) == "" {
		return devdata.DefaultFetchCheckpointPath(baseDir)
	}
	return o.checkpoint
}

func parseCommandFlags(command string, args []string, stderr io.Writer, destructive bool) (commandOptions, error) {
	batchSize, err := fetchIntEnv("DEVDATA_FETCH_BATCH_SIZE", DefaultCommandBatchSize)
	if err != nil {
		return commandOptions{}, err
	}
	batchDelay, err := fetchDurationEnv("DEVDATA_FETCH_BATCH_DELAY", DefaultCommandBatchDelay)
	if err != nil {
		return commandOptions{}, err
	}
	options := commandOptions{
		source:     "rsshub",
		profile:    "core",
		batchSize:  batchSize,
		batchDelay: batchDelay,
	}
	if command == "refresh-incremental" || command == "refresh-account" {
		options.fetchCount, err = fetchIntEnv("DEVDATA_INCREMENTAL_FETCH_COUNT", devdata.DefaultRSSHubIncrementalFetchCount)
		if err != nil {
			return commandOptions{}, err
		}
		options.shard = "auto"
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.source, "source", options.source, "source adapter (x or rsshub)")
	flags.StringVar(&options.profile, "profile", options.profile, "DevData profile")
	flags.BoolVar(&options.allowDestructive, "allow-destructive", false, "allow desired-state retirement/deletion")
	flags.StringVar(&options.registry, "registry", "", "source registry path (operator/test override)")
	flags.StringVar(&options.snapshot, "snapshot", "", "snapshot path (operator/test override)")
	flags.StringVar(&options.checkpoint, "checkpoint", "", "fetch checkpoint path (operator/test override)")
	flags.BoolVar(&options.resetCheckpoint, "reset-checkpoint", false, "remove the existing fetch checkpoint before fetching")
	flags.IntVar(&options.batchSize, "batch-size", options.batchSize, "RSSHub accounts per sequential batch")
	flags.DurationVar(&options.batchDelay, "batch-delay", options.batchDelay, "delay between RSSHub batches")
	if command == "refresh-incremental" {
		flags.StringVar(&options.shard, "shard", options.shard, "incremental shard (auto or 0-3)")
		flags.IntVar(&options.fetchCount, "fetch-count", options.fetchCount, "incremental source window (5-60)")
	}
	if command == "refresh-account" {
		flags.StringVar(&options.key, "key", options.key, "enabled source registry key to refresh")
		flags.IntVar(&options.fetchCount, "fetch-count", options.fetchCount, "targeted source window (5-60)")
	}
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	options.source = strings.ToLower(strings.TrimSpace(options.source))
	if options.source != "x" && options.source != "rsshub" {
		return commandOptions{}, errors.New("--source must be x or rsshub")
	}
	if strings.ToLower(strings.TrimSpace(options.profile)) != "core" {
		return commandOptions{}, errors.New("only --profile=core is supported")
	}
	if destructive && !options.allowDestructive {
		return commandOptions{}, fmt.Errorf("%s requires --allow-destructive", command)
	}
	if command == "refresh-incremental" {
		if options.allowDestructive {
			return commandOptions{}, errors.New("refresh-incremental does not support --allow-destructive")
		}
		if options.resetCheckpoint {
			return commandOptions{}, errors.New("refresh-incremental does not support --reset-checkpoint")
		}
		if options.fetchCount < 5 || options.fetchCount > devdata.DefaultRSSHubFullFetchCount {
			return commandOptions{}, fmt.Errorf("--fetch-count must be between 5 and %d", devdata.DefaultRSSHubFullFetchCount)
		}
		if _, err := devdata.ParseIncrementalShard(options.shard, time.Now().UTC()); err != nil {
			return commandOptions{}, fmt.Errorf("--shard: %w", err)
		}
	}
	if command == "refresh-account" {
		if strings.TrimSpace(options.key) == "" {
			return commandOptions{}, errors.New("--key is required for refresh-account")
		}
		if options.allowDestructive {
			return commandOptions{}, errors.New("refresh-account does not support --allow-destructive")
		}
		if options.resetCheckpoint {
			return commandOptions{}, errors.New("refresh-account does not support --reset-checkpoint")
		}
		if options.fetchCount < 5 || options.fetchCount > devdata.DefaultRSSHubFullFetchCount {
			return commandOptions{}, fmt.Errorf("--fetch-count must be between 5 and %d", devdata.DefaultRSSHubFullFetchCount)
		}
	}
	if options.batchSize < 1 {
		return commandOptions{}, errors.New("--batch-size must be at least 1")
	}
	if options.batchDelay < 0 {
		return commandOptions{}, errors.New("--batch-delay must be non-negative")
	}
	return options, nil
}

const (
	DefaultCommandBatchSize  = devdata.DefaultSourceBatchSize
	DefaultCommandBatchDelay = devdata.DefaultRSSHubBatchDelay
)

func fetchIntEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return value, nil
}

func fetchDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 60s: %w", name, err)
	}
	return value, nil
}

func loadCuratedRegistry(path string) (devdata.SourceRegistry, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return devdata.SourceRegistry{}, fmt.Errorf("resolve source registry path: %w", err)
	}
	return devdata.LoadCuratedRegistry(absolute)
}

func newLiveSource(source string) (devdata.SnapshotSourceClient, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "rsshub":
		return devdata.NewRSSHubClientFromEnv()
	case "x":
		if strings.TrimSpace(os.Getenv("X_BEARER_TOKEN")) == "" {
			return nil, errors.New("LIVE_X_ACTIVATION_BLOCKED: X_BEARER_TOKEN unavailable")
		}
		return devdata.NewXClientFromEnv()
	default:
		return nil, errors.New("unsupported source adapter")
	}
}

func fetchSnapshotForCommand(ctx context.Context, client devdata.SnapshotSourceClient, registry devdata.SourceRegistry, options commandOptions, baseDir string, stdout io.Writer) (devdata.Snapshot, devdata.FetchReport, error) {
	if options.source == "rsshub" {
		return devdata.FetchRSSHubResumable(ctx, client, registry, devdata.ResumableFetchOptions{
			BatchSize:       options.batchSize,
			BatchDelay:      options.batchDelay,
			FetchCount:      devdata.DefaultRSSHubFullFetchCount,
			CheckpointPath:  options.checkpointPath(baseDir),
			SnapshotPath:    options.snapshotPath(baseDir),
			ResetCheckpoint: options.resetCheckpoint,
			Progress: func(message string) {
				fmt.Fprintln(stdout, message)
			},
		})
	}
	snapshot, report, err := devdata.FetchSnapshot(ctx, client, registry, time.Now().UTC())
	if err != nil {
		return devdata.Snapshot{}, report, err
	}
	if err := devdata.WriteSnapshotAtomic(options.snapshotPath(baseDir), snapshot, registry); err != nil {
		return devdata.Snapshot{}, report, err
	}
	return snapshot, report, nil
}

func runIncrementalRefresh(ctx context.Context, baseDir string, options commandOptions, stdout, stderr io.Writer) error {
	db, err := initDatabase()
	if err != nil {
		return err
	}
	lock, acquired, err := devdata.TryAcquireDevDataMutationLock(ctx, db)
	if err != nil {
		return err
	}
	if !acquired {
		_, _ = io.WriteString(stdout, "Incremental refresh skipped: another DevData mirror mutation is active\n")
		return nil
	}
	defer func() {
		if releaseErr := lock.Release(context.Background()); releaseErr != nil {
			fmt.Fprintf(stderr, "WARN: release DevData mutation lock: %v\n", releaseErr)
		}
	}()

	registry, err := loadCuratedRegistry(options.registryPath(baseDir))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	shard, err := devdata.ParseIncrementalShard(options.shard, now)
	if err != nil {
		return fmt.Errorf("--shard: %w", err)
	}
	selected, err := devdata.SelectIncrementalShardAccounts(registry, shard)
	if err != nil {
		return err
	}
	selectedKeys := make([]string, 0, len(selected))
	for _, account := range selected {
		selectedKeys = append(selectedKeys, account.Key)
	}
	snapshotPath := options.snapshotPath(baseDir)
	baseline, baselineFingerprint, err := devdata.ReadIncrementalBaseline(snapshotPath, registry)
	if err != nil {
		return err
	}
	client, err := newLiveSource(options.source)
	if err != nil {
		return err
	}
	batch, fetchReport, err := devdata.FetchIncrementalBatchWithOptions(ctx, client, registry, baseline, devdata.IncrementalFetchOptions{
		Shard:      shard,
		FetchCount: options.fetchCount,
		FetchedAt:  now,
		Progress: func(message string) {
			fmt.Fprintln(stdout, message)
		},
	})
	if err != nil {
		return err
	}
	if err := devdata.ValidateIncrementalBatch(batch, registry); err != nil {
		return err
	}
	nextSnapshot, err := devdata.MergeIncrementalSnapshot(baseline, batch, registry, now)
	if err != nil {
		return err
	}

	redisClient := bestEffortRedis(stderr)
	if redisClient != nil {
		defer redisClient.Close()
	}
	var mirrorStore devdata.AvatarObjectStore
	storageClient, storageErr := config.NewStorageClient()
	if storageErr != nil {
		fmt.Fprintln(stderr, "WARN: mirror storage unavailable; incremental avatar, cover, and PostMedia localization will be skipped")
	} else {
		mirrorStore, storageErr = devdata.NewMinioAvatarObjectStore(storageClient)
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: mirror storage adapter unavailable; incremental avatar, cover, and PostMedia localization will be skipped")
		}
	}
	var avatarFetcher devdata.AvatarFetcher
	var coverFetcher devdata.CoverFetcher
	var postMediaFetcher devdata.PostMediaFetcher
	if mirrorStore != nil {
		avatarFetcher = devdata.NewAvatarDownloader()
		coverFetcher = devdata.NewCoverDownloader()
		postMediaFetcher = devdata.NewPostMediaDownloader()
	}
	avatarSnapshot := devdata.Snapshot{Version: devdata.DefaultSnapshotVersion, FetchedAt: batch.FetchedAt, Accounts: batch.Accounts}
	avatarResolutions, avatarReport, err := devdata.PrepareAvatarMirrorsForKeys(ctx, registry, avatarSnapshot, selectedKeys, avatarFetcher, mirrorStore)
	if err != nil {
		return err
	}
	coverResolutions, coverReport, err := devdata.PrepareCoverMirrorsForKeys(ctx, registry, avatarSnapshot, selectedKeys, coverFetcher, mirrorStore)
	if err != nil {
		return err
	}
	postMediaResolutions, postMediaReport, err := devdata.PrepareIncrementalPostMediaMirrors(ctx, db, registry, batch, postMediaFetcher, mirrorStore)
	if err != nil {
		return err
	}
	result, err := devdata.SyncIncremental(ctx, db, registry, batch, redisClient, now, devdata.SyncOptions{
		AvatarResolutions:                    avatarResolutions,
		CoverResolutions:                     coverResolutions,
		PostMediaResolutions:                 postMediaResolutions,
		PreserveExistingAvatarWhenUnresolved: true,
		PreserveExistingCoverWhenUnresolved:  true,
	})
	if err != nil {
		return err
	}
	if err := devdata.WriteIncrementalSnapshotIfUnchanged(snapshotPath, baselineFingerprint, nextSnapshot, registry); err != nil {
		if errors.Is(err, devdata.ErrIncrementalSnapshotChanged) {
			fmt.Fprintln(stderr, "Incremental refresh aborted: rolling snapshot changed during this run; retry against the new baseline")
			return err
		}
		return fmt.Errorf("write incremental snapshot: %w", err)
	}
	writeIncrementalSummary(stdout, shard, fetchReport, result, avatarReport, coverReport, postMediaReport, nextSnapshot)
	writeMediaLocalizationWarning(stderr, avatarReport.Failed, postMediaReport.Failed)
	writeCoverLocalizationWarning(stderr, coverReport.Failed)
	return nil
}

func writeIncrementalSummary(stdout io.Writer, shard int, fetchReport devdata.IncrementalFetchReport, syncResult devdata.SyncResult, avatarReport devdata.AvatarMirrorReport, coverReport devdata.CoverMirrorReport, postMediaReport devdata.PostMediaMirrorReport, snapshot devdata.Snapshot) {
	fmt.Fprintf(stdout, "Incremental refresh: shard=%d/%d accounts=%d account_refresh_interval≈4h fetch_count=%d escalated_to_60=%d coverage_window_exhausted=%d\n", shard, devdata.IncrementalShardCount, fetchReport.Accounts, fetchReport.FetchCount, fetchReport.EscalatedToFull, fetchReport.CoverageWindowExhausted)
	fmt.Fprintf(stdout, "Source: requests=%d posts_returned=%d scanned=%d eligible=%d\n", fetchReport.APIRequests, fetchReport.SourcePostsReturned, fetchReport.SourcePostsScanned, fetchReport.EligibleSelected)
	fmt.Fprintf(stdout, "Sync: new_posts=%d existing_posts=%d reactivated=%d media_uploaded=%d media_reused=%d avatars_uploaded=%d avatars_reused=%d covers_uploaded=%d covers_reused=%d covers_cleared=%d\n", syncResult.Inserted, syncResult.Kept, syncResult.Reactivated, postMediaReport.Uploaded, postMediaReport.Reused, avatarReport.Uploaded, avatarReport.Reused, coverReport.Uploaded, coverReport.Reused, coverReport.Cleared)
	fmt.Fprintf(stdout, "Snapshot: accounts=%d posts=%d\n", len(snapshot.Accounts), len(snapshot.Posts))
}

func initDatabase() (*gorm.DB, error) {
	config.InitDatabaseConfig()
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	return global.Db, nil
}

func bestEffortRedis(stderr io.Writer) *redis.Client {
	client, err := config.NewRedisClient()
	if err != nil {
		fmt.Fprintf(stderr, "WARN: Redis unavailable; DB sync remains committed without cache/like maintenance: %v\n", err)
		return nil
	}
	return client
}

func syncAndVerify(stdout, stderr io.Writer, registry devdata.SourceRegistry, snapshot devdata.Snapshot, options devdata.SyncOptions) error {
	db, err := initDatabase()
	if err != nil {
		return err
	}
	redisClient := bestEffortRedis(stderr)
	if redisClient != nil {
		defer redisClient.Close()
	}
	return syncAndVerifyWithDB(stdout, registry, snapshot, db, redisClient, options)
}

func syncAndVerifyWithDB(stdout io.Writer, registry devdata.SourceRegistry, snapshot devdata.Snapshot, db *gorm.DB, redisClient *redis.Client, options devdata.SyncOptions) error {
	result, err := devdata.SyncSnapshotWithOptions(context.Background(), db, registry, snapshot, redisClient, time.Now().UTC(), options)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Sync: kept=%d reactivated=%d inserted=%d retired_soft=%d retired_hard=%d\n", result.Kept, result.Reactivated, result.Inserted, result.RetiredSoft, result.RetiredHard)
	verification, err := devdata.VerifyCoreWithOptions(context.Background(), db, registry, time.Now().UTC(), devdata.VerificationOptions{Mode: devdata.VerificationModeCuratedV1Live})
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, devdata.FormatCoreVerification(verification))
	return err
}

func writeFetchReport(stdout io.Writer, report devdata.FetchReport, snapshotPath string) {
	fmt.Fprintf(stdout, "Snapshot written: %s\n", snapshotPath)
	writeFetchReportSummary(stdout, report)
}

func writeFetchReportSummary(stdout io.Writer, report devdata.FetchReport) {
	fmt.Fprintf(stdout, "API requests (current run): %d\n", report.APIRequests)
	fmt.Fprintf(stdout, "Source Posts scanned: %d\n", report.SourcePostsScanned)
	fmt.Fprintf(stdout, "Eligible Posts selected: %d\n", report.EligibleSelected)
	for _, account := range report.PerAccount {
		fmt.Fprintf(stdout, "%s: requests=%d scanned=%d selected=%d\n", account.RegistryKey, account.APIRequests, account.SourcePostsScanned, account.EligibleSelected)
	}
}

func writeMediaLocalizationWarning(stderr io.Writer, avatarFailures, postMediaFailures int) {
	if avatarFailures == 0 && postMediaFailures == 0 {
		return
	}
	fmt.Fprintf(stderr, "WARN: media localization completed with failures: avatars=%d post_media=%d\n", avatarFailures, postMediaFailures)
}

func writeCoverLocalizationWarning(stderr io.Writer, coverFailures int) {
	if coverFailures == 0 {
		return
	}
	fmt.Fprintf(stderr, "WARN: cover localization completed with failures: covers=%d\n", coverFailures)
}
