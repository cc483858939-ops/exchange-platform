package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

type recoveryOptions struct {
	PostID uint
	Apply  bool
}

func run(args []string, stdout io.Writer) error {
	options, err := parseRecoveryOptions(args)
	if err != nil {
		return err
	}
	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	if global.MaintenanceDb == nil {
		return errors.New("maintenance database is not initialized")
	}
	redisClient, err := config.NewRedisClient()
	if err != nil {
		return fmt.Errorf("Redis is required: %w", err)
	}
	defer redisClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return recoverPostLikeState(ctx, global.MaintenanceDb, redisClient, options.PostID, options.Apply, stdout)
}

func parseRecoveryOptions(args []string) (recoveryOptions, error) {
	flags := flag.NewFlagSet("recover-post-like-state", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	postID := flags.Uint64("post-id", 0, "active PostID whose trusted new-Post Like initialization failed")
	apply := flags.Bool("apply", false, "apply the initialization; without this flag the command is a dry run")
	confirmDevelopment := flags.Bool("confirm-development-reset", false, "confirm this is an explicitly approved development recovery and Like history is disposable")
	confirmWritesPaused := flags.Bool("confirm-like-writes-paused", false, "confirm API and Like workers are stopped or reject new Like writes")
	confirmQueuesDrained := flags.Bool("confirm-snapshot-behavior-kafka-drained", false, "confirm Snapshot/Behavior queues and Kafka lag have been reviewed and drained")
	if err := flags.Parse(args); err != nil {
		return recoveryOptions{}, err
	}
	if flags.NArg() != 0 {
		return recoveryOptions{}, errors.New("unexpected positional arguments")
	}
	if *postID == 0 {
		return recoveryOptions{}, errors.New("--post-id must be a positive PostID")
	}
	if *apply && (!*confirmDevelopment || !*confirmWritesPaused || !*confirmQueuesDrained) {
		return recoveryOptions{}, errors.New("refusing mutation without development-reset, Like-write-paused, and Snapshot/Behavior/Kafka-drained confirmations")
	}
	return recoveryOptions{PostID: uint(*postID), Apply: *apply}, nil
}

func recoverPostLikeState(ctx context.Context, db *gorm.DB, client *redis.Client, postID uint, apply bool, stdout io.Writer) error {
	if postID == 0 || db == nil || client == nil {
		return errors.New("PostID, PostgreSQL, and Redis are required")
	}
	ready, err := validateNewPostRecoveryCandidate(ctx, db, client, postID, false)
	if err != nil {
		return err
	}
	if ready {
		_, err := fmt.Fprintf(stdout, "PostID=%d already_ready=true; no mutation performed\n", postID)
		return err
	}
	if !apply {
		_, err := fmt.Fprintf(stdout, "DRY RUN: PostID=%d passed safety checks; no Redis state changed. Use --apply with all explicit maintenance confirmations to initialize.\n", postID)
		return err
	}

	store := likes.NewStore(client)
	created, err := store.InitializeNewPostFrom(ctx, postID, func(loadCtx context.Context) (likes.FullState, error) {
		alreadyReady, validateErr := validateNewPostRecoveryCandidate(loadCtx, db, client, postID, true)
		if validateErr != nil {
			return likes.FullState{}, validateErr
		}
		if alreadyReady {
			return likes.FullState{}, nil
		}
		return likes.FullState{}, nil
	})
	if err != nil {
		return fmt.Errorf("initialize Post %d Like state: %w", postID, err)
	}
	_, err = fmt.Fprintf(stdout, "PostID=%d initialized=%t ready=true\n", postID, created)
	return err
}

func validateNewPostRecoveryCandidate(ctx context.Context, db *gorm.DB, client *redis.Client, postID uint, tokenHeld bool) (bool, error) {
	var post models.Post
	if err := db.WithContext(ctx).Where("id = ?", postID).First(&post).Error; err != nil {
		return false, fmt.Errorf("load active Post %d: %w", postID, err)
	}
	if post.LikeCount != 0 || post.LikeSyncVersion != 0 {
		return false, fmt.Errorf("Post %d has SQL Like history (count=%d version=%d); refusing zero initialization", postID, post.LikeCount, post.LikeSyncVersion)
	}
	var reactions int64
	if err := db.WithContext(ctx).Model(&models.PostReaction{}).Where("post_id = ?", postID).Count(&reactions).Error; err != nil {
		return false, fmt.Errorf("inspect Post %d reactions: %w", postID, err)
	}
	if reactions != 0 {
		return false, fmt.Errorf("Post %d has %d projected Like reaction rows; refusing zero initialization", postID, reactions)
	}

	keys := []struct{ key, expected string }{
		{likes.ReadyKey(postID), "string"},
		{likes.CountKey(postID), "string"},
		{likes.VersionKey(postID), "string"},
		{likes.RebuildTokenKey(postID), "string"},
		{likes.RegistryKey, "set"},
		{likes.DirtyKey, "set"},
		{likes.ProcessingKey, "zset"},
		{likes.ClaimsKey, "hash"},
		{likes.ExpiryCandidatesKey, "zset"},
		{likes.RecoverableVersionsKey, "hash"},
		{likes.BehaviorDirtyKey, "set"},
		{likes.BehaviorStateKey, "hash"},
		{likes.BehaviorProcessingKey, "zset"},
		{likes.BehaviorClaimsKey, "hash"},
	}
	for _, entry := range keys {
		kind, err := client.WithContext(ctx).Type(entry.key).Result()
		if err != nil {
			return false, fmt.Errorf("inspect Redis key type %q: %w", entry.key, err)
		}
		if kind != "none" && kind != entry.expected {
			return false, fmt.Errorf("Redis key %q has type %q, expected %q", entry.key, kind, entry.expected)
		}
	}
	if kind, err := client.WithContext(ctx).Type(likes.UsersKey(postID)).Result(); err != nil {
		return false, fmt.Errorf("inspect legacy Post Users key: %w", err)
	} else if kind != "none" {
		return false, fmt.Errorf("legacy Post Users key exists for Post %d; refusing recovery", postID)
	}

	readyValue, readyErr := client.WithContext(ctx).Get(likes.ReadyKey(postID)).Result()
	if readyErr == nil {
		if readyValue == "deleted" {
			return false, likes.ErrPostLikeUnavailable
		}
		if readyValue != "1" {
			return false, fmt.Errorf("Post %d has unexpected Redis Ready value %q", postID, readyValue)
		}
		count, countErr := client.WithContext(ctx).Get(likes.CountKey(postID)).Result()
		version, versionErr := client.WithContext(ctx).Get(likes.VersionKey(postID)).Result()
		if countErr != nil || versionErr != nil {
			return false, fmt.Errorf("Post %d Ready state is incomplete: count=%v version=%v", postID, countErr, versionErr)
		}
		countValue, err := strconv.ParseInt(count, 10, 64)
		if err != nil {
			return false, fmt.Errorf("Post %d Count is invalid: %w", postID, err)
		}
		if countValue < 0 {
			return false, fmt.Errorf("Post %d Count is negative", postID)
		}
		versionValue, err := strconv.ParseInt(version, 10, 64)
		if err != nil {
			return false, fmt.Errorf("Post %d Version is invalid: %w", postID, err)
		}
		if versionValue < 0 {
			return false, fmt.Errorf("Post %d Version is negative", postID)
		}
		return true, nil
	}
	if readyErr != redis.Nil {
		return false, fmt.Errorf("read Post %d Ready state: %w", postID, readyErr)
	}
	for _, key := range []string{likes.CountKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.WithContext(ctx).Exists(key).Result(); err != nil {
			return false, fmt.Errorf("inspect Redis key %q: %w", key, err)
		} else if exists != 0 {
			return false, fmt.Errorf("Post %d has partial Redis state at %q; refusing initialization", postID, key)
		}
	}

	tokenExists, err := client.WithContext(ctx).Exists(likes.RebuildTokenKey(postID)).Result()
	if err != nil {
		return false, fmt.Errorf("inspect Post %d rebuild token: %w", postID, err)
	}
	if tokenHeld != (tokenExists == 1) {
		return false, fmt.Errorf("Post %d rebuild token state changed during recovery preflight", postID)
	}

	postIDString := fmt.Sprint(postID)
	if member, err := client.WithContext(ctx).SIsMember(likes.RegistryKey, postIDString).Result(); err != nil || member {
		if err != nil {
			return false, fmt.Errorf("inspect Post registry: %w", err)
		}
		return false, fmt.Errorf("Post %d is already in the Redis Like registry", postID)
	}
	if member, err := client.WithContext(ctx).SIsMember(likes.DirtyKey, postIDString).Result(); err != nil || member {
		if err != nil {
			return false, fmt.Errorf("inspect Snapshot Dirty queue: %w", err)
		}
		return false, fmt.Errorf("Post %d remains in the Snapshot Dirty queue", postID)
	}
	if _, err := client.WithContext(ctx).ZScore(likes.ProcessingKey, postIDString).Result(); err == nil {
		return false, fmt.Errorf("Post %d remains in Snapshot processing", postID)
	} else if err != redis.Nil {
		return false, fmt.Errorf("inspect Snapshot processing: %w", err)
	}
	if exists, err := client.WithContext(ctx).HExists(likes.ClaimsKey, postIDString).Result(); err != nil || exists {
		if err != nil {
			return false, fmt.Errorf("inspect Snapshot claims: %w", err)
		}
		return false, fmt.Errorf("Post %d has an active Snapshot claim", postID)
	}
	if _, err := client.WithContext(ctx).ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err == nil {
		return false, fmt.Errorf("Post %d remains in Like expiry candidates", postID)
	} else if err != redis.Nil {
		return false, fmt.Errorf("inspect Like expiry candidates: %w", err)
	}
	if exists, err := client.WithContext(ctx).HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || exists {
		if err != nil {
			return false, fmt.Errorf("inspect recoverable version marker: %w", err)
		}
		return false, fmt.Errorf("Post %d has an existing recoverable version marker", postID)
	}

	behaviorDirty, err := client.WithContext(ctx).SCard(likes.BehaviorDirtyKey).Result()
	if err != nil {
		return false, fmt.Errorf("inspect Behavior Dirty queue: %w", err)
	}
	behaviorProcessing, err := client.WithContext(ctx).ZCard(likes.BehaviorProcessingKey).Result()
	if err != nil {
		return false, fmt.Errorf("inspect Behavior processing: %w", err)
	}
	behaviorClaims, err := client.WithContext(ctx).HLen(likes.BehaviorClaimsKey).Result()
	if err != nil {
		return false, fmt.Errorf("inspect Behavior claims: %w", err)
	}
	behaviorStates, err := client.WithContext(ctx).HLen(likes.BehaviorStateKey).Result()
	if err != nil {
		return false, fmt.Errorf("inspect Behavior state: %w", err)
	}
	if behaviorDirty != 0 || behaviorProcessing != 0 || behaviorClaims != 0 || behaviorStates != 0 {
		return false, fmt.Errorf("global Behavior queues are not empty (dirty=%d processing=%d claims=%d states=%d)", behaviorDirty, behaviorProcessing, behaviorClaims, behaviorStates)
	}
	return false, nil
}
