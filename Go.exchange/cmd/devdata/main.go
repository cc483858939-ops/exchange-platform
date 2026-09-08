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
		return errors.New("usage: go run ./cmd/devdata <preflight|fetch|refresh|rebuild|verify|verify-avatars> [flags]")
	}
	baseDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	switch args[0] {
	case "preflight":
		options, err := parseCommandFlags("preflight", args[1:], stderr, false)
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
		var results []devdata.PreflightResult
		var preflightErr error
		if options.source == "rsshub" {
			results, preflightErr = devdata.PreflightRSSHubSources(context.Background(), client, registry, devdata.ResumableFetchOptions{
				BatchSize:  options.batchSize,
				BatchDelay: options.batchDelay,
				Progress: func(message string) {
					fmt.Fprintln(stdout, message)
				},
			})
		} else {
			results, preflightErr = devdata.PreflightSources(context.Background(), client, registry)
		}
		_, _ = io.WriteString(stdout, devdata.FormatPreflightResults(results))
		return preflightErr
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
		db, err := initDatabase()
		if err != nil {
			return err
		}
		redisClient := bestEffortRedis(stderr)
		if redisClient != nil {
			defer redisClient.Close()
		}
		var avatarStore devdata.AvatarObjectStore
		storageClient, storageErr := config.NewStorageClient()
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: avatar storage unavailable; avatar localization will be skipped")
		} else {
			avatarStore, err = devdata.NewMinioAvatarObjectStore(storageClient)
			if err != nil {
				fmt.Fprintln(stderr, "WARN: avatar storage adapter unavailable; avatar localization will be skipped")
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
			PostMediaResolutions:                 postMediaResolutions,
			PreserveExistingAvatarWhenUnresolved: true,
		}); err != nil {
			return err
		}
		writeMediaLocalizationWarning(stderr, avatarReport.Failed, postMediaReport.Failed)
		return nil
	case "rebuild":
		options, err := parseCommandFlags("rebuild", args[1:], stderr, true)
		if err != nil {
			return err
		}
		registry, err := loadCuratedRegistry(options.registryPath(baseDir))
		if err != nil {
			return err
		}
		snapshot, err := devdata.ReadSnapshot(options.snapshotPath(baseDir), registry)
		if err != nil {
			return err
		}
		var postMediaStore devdata.AvatarObjectStore
		storageClient, storageErr := config.NewStorageClient()
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: post media storage unavailable; existing PostMedia rows will be preserved for unresolved images")
		} else {
			postMediaStore, storageErr = devdata.NewMinioAvatarObjectStore(storageClient)
			if storageErr != nil {
				fmt.Fprintln(stderr, "WARN: post media storage adapter unavailable; existing PostMedia rows will be preserved for unresolved images")
			}
		}
		var postMediaFetcher devdata.PostMediaFetcher
		if postMediaStore != nil {
			postMediaFetcher = devdata.NewPostMediaDownloader()
		}
		postMediaResolutions, postMediaReport, err := devdata.PreparePostMediaMirrors(context.Background(), registry, snapshot, postMediaFetcher, postMediaStore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Post media: posts=%d attempted=%d uploaded=%d reused=%d failed=%d\n", postMediaReport.PostsWithMedia, postMediaReport.Attempted, postMediaReport.Uploaded, postMediaReport.Reused, postMediaReport.Failed)
		if err := syncAndVerify(stdout, stderr, registry, snapshot, devdata.SyncOptions{
			PostMediaResolutions:                 postMediaResolutions,
			PreserveExistingAvatarWhenUnresolved: true,
		}); err != nil {
			return err
		}
		writeMediaLocalizationWarning(stderr, 0, postMediaReport.Failed)
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
