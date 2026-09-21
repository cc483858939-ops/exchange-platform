package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"Go.exchange/config"
	"Go.exchange/devdata"
)

func runTargetedRefresh(ctx context.Context, baseDir string, options commandOptions, stdout, stderr io.Writer) error {
	db, err := initDatabase()
	if err != nil {
		return err
	}
	lock, err := devdata.AcquireDevDataMutationLock(ctx, db)
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
	if _, err := devdata.ResolveTargetedAccount(registry, options.key); err != nil {
		return err
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
	now := time.Now().UTC()
	batch, fetchReport, err := devdata.FetchTargetedAccount(ctx, client, registry, baseline, devdata.TargetedRefreshOptions{
		RegistryKey: options.key,
		FetchCount:  options.fetchCount,
		FetchedAt:   now,
		Progress: func(message string) {
			fmt.Fprintln(stdout, message)
		},
	})
	if err != nil {
		return err
	}
	nextSnapshot, err := devdata.MergeTargetedAccountSnapshot(baseline, batch, registry, now)
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
		fmt.Fprintln(stderr, "WARN: mirror storage unavailable; targeted avatar, cover, and PostMedia localization will be skipped")
	} else {
		mirrorStore, storageErr = devdata.NewMinioAvatarObjectStore(storageClient)
		if storageErr != nil {
			fmt.Fprintln(stderr, "WARN: mirror storage adapter unavailable; targeted avatar, cover, and PostMedia localization will be skipped")
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
	selectedKeys := []string{batch.RegistryKey}
	avatarResolutions, avatarReport, err := devdata.PrepareAvatarMirrorsForKeys(ctx, registry, nextSnapshot, selectedKeys, avatarFetcher, mirrorStore)
	if err != nil {
		return err
	}
	coverResolutions, coverReport, err := devdata.PrepareCoverMirrorsForKeys(ctx, registry, nextSnapshot, selectedKeys, coverFetcher, mirrorStore)
	if err != nil {
		return err
	}
	postMediaResolutions, postMediaReport, err := devdata.PrepareTargetedPostMediaMirrors(ctx, db, registry, batch, postMediaFetcher, mirrorStore)
	if err != nil {
		return err
	}
	result, err := devdata.SyncTargeted(ctx, db, registry, batch, redisClient, now, devdata.SyncOptions{
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
			fmt.Fprintln(stderr, "Targeted refresh aborted: rolling snapshot changed during this run; retry against the new baseline")
			return err
		}
		return fmt.Errorf("write targeted snapshot: %w", err)
	}

	fmt.Fprintf(stdout, "Targeted refresh: key=%s\n", batch.RegistryKey)
	fmt.Fprintf(stdout, "Source: requests=%d scanned=%d eligible=%d window=%d escalated_to_full=%t coverage_window_exhausted=%t\n", fetchReport.APIRequests, fetchReport.SourcePostsScanned, fetchReport.EligibleSelected, fetchReport.FetchCount, fetchReport.EscalatedToFull, fetchReport.CoverageWindowExhausted)
	fmt.Fprintf(stdout, "Avatar: uploaded=%d reused=%d failed=%d\n", avatarReport.Uploaded, avatarReport.Reused, avatarReport.Failed)
	fmt.Fprintf(stdout, "Cover: uploaded=%d reused=%d cleared=%d failed=%d\n", coverReport.Uploaded, coverReport.Reused, coverReport.Cleared, coverReport.Failed)
	fmt.Fprintf(stdout, "Post media: uploaded=%d reused=%d failed=%d\n", postMediaReport.Uploaded, postMediaReport.Reused, postMediaReport.Failed)
	fmt.Fprintf(stdout, "Sync: new_posts=%d existing_posts=%d reactivated=%d\n", result.Inserted, result.Kept, result.Reactivated)
	fmt.Fprintf(stdout, "Snapshot: accounts=%d posts=%d\n", len(nextSnapshot.Accounts), len(nextSnapshot.Posts))
	writeMediaLocalizationWarning(stderr, avatarReport.Failed, postMediaReport.Failed)
	writeCoverLocalizationWarning(stderr, coverReport.Failed)
	return nil
}
