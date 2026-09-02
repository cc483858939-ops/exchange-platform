package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		return errors.New("usage: go run ./cmd/devdata <preflight|fetch|refresh|rebuild|verify> [flags]")
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
		results, preflightErr := devdata.PreflightSources(context.Background(), client, registry)
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
		snapshot, report, err := devdata.FetchSnapshot(context.Background(), client, registry, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := devdata.WriteSnapshotAtomic(options.snapshotPath(baseDir), snapshot, registry); err != nil {
			return err
		}
		writeFetchReport(stdout, report, options.snapshotPath(baseDir))
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
		snapshot, report, err := devdata.FetchSnapshot(context.Background(), client, registry, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := devdata.WriteSnapshotAtomic(options.snapshotPath(baseDir), snapshot, registry); err != nil {
			return err
		}
		writeFetchReport(stdout, report, options.snapshotPath(baseDir))
		return syncAndVerify(stdout, stderr, registry, snapshot)
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
		return syncAndVerify(stdout, stderr, registry, snapshot)
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

func parseCommandFlags(command string, args []string, stderr io.Writer, destructive bool) (commandOptions, error) {
	options := commandOptions{source: "rsshub", profile: "core"}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.source, "source", options.source, "source adapter (x or rsshub)")
	flags.StringVar(&options.profile, "profile", options.profile, "DevData profile")
	flags.BoolVar(&options.allowDestructive, "allow-destructive", false, "allow desired-state retirement/deletion")
	flags.StringVar(&options.registry, "registry", "", "source registry path (operator/test override)")
	flags.StringVar(&options.snapshot, "snapshot", "", "snapshot path (operator/test override)")
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
	return options, nil
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

func syncAndVerify(stdout, stderr io.Writer, registry devdata.SourceRegistry, snapshot devdata.Snapshot) error {
	db, err := initDatabase()
	if err != nil {
		return err
	}
	redisClient := bestEffortRedis(stderr)
	if redisClient != nil {
		defer redisClient.Close()
	}
	result, err := devdata.SyncSnapshot(context.Background(), db, registry, snapshot, redisClient, time.Now().UTC())
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
	fmt.Fprintf(stdout, "API requests: %d\n", report.APIRequests)
	fmt.Fprintf(stdout, "Source Posts scanned: %d\n", report.SourcePostsScanned)
	fmt.Fprintf(stdout, "Eligible Posts selected: %d\n", report.EligibleSelected)
	for _, account := range report.PerAccount {
		fmt.Fprintf(stdout, "%s: requests=%d scanned=%d selected=%d\n", account.RegistryKey, account.APIRequests, account.SourcePostsScanned, account.EligibleSelected)
	}
}
