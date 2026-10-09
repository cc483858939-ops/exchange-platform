package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const maxUserPageSize = 1000

type initReport struct {
	Initialized  int64
	AlreadyReady int64
	Failed       int64
	LastUserID   uint
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("initialize-user-likes", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmReset := flags.Bool("confirm-development-reset", false, "confirm this is an unlaunched development environment and no Like history must be preserved")
	confirmQuiesced := flags.Bool("confirm-api-worker-kafka-quiesced", false, "confirm API and Like workers are stopped and pending Kafka/projection work has been reviewed")
	pageSize := flags.Int("page-size", 200, "SQL UserID page size (1-1000)")
	startAfter := flags.Uint("start-after-user-id", 0, "resume after this UserID; reruns from zero are also idempotent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if !*confirmReset || !*confirmQuiesced {
		return errors.New("refusing stock User Like initialization without both explicit development-reset and quiesced-worker confirmations")
	}
	if *pageSize < 1 || *pageSize > maxUserPageSize {
		return fmt.Errorf("page-size must be between 1 and %d", maxUserPageSize)
	}

	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	if global.MaintenanceDb == nil {
		return errors.New("maintenance database is not initialized")
	}
	if err := validateNoLikeProjectionHistory(context.Background(), global.MaintenanceDb); err != nil {
		return fmt.Errorf("refusing User Like initialization: %w", err)
	}
	redisClient, err := config.NewRedisClient()
	if err != nil {
		return fmt.Errorf("Redis is required: %w", err)
	}
	defer redisClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, runErr := initializeUsers(ctx, global.MaintenanceDb, likes.NewStore(redisClient), *pageSize, *startAfter, stdout)
	if _, err := fmt.Fprintf(stdout, "Complete: initialized=%d already_ready=%d failed=%d last_user_id=%d\n",
		report.Initialized, report.AlreadyReady, report.Failed, report.LastUserID); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if report.Failed > 0 {
		return errors.New("stock User Like initialization completed with failures; rerun from UserID 0 for safe idempotent retries")
	}
	return nil
}

func validateNoLikeProjectionHistory(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	var nonZeroPostStates int64
	if err := db.WithContext(ctx).Unscoped().Model(&models.Post{}).
		Where("like_count <> 0 OR like_sync_version <> 0").Count(&nonZeroPostStates).Error; err != nil {
		return fmt.Errorf("inspect Post Like projections: %w", err)
	}
	var likeReactions int64
	if err := db.WithContext(ctx).Model(&models.PostReaction{}).
		Where("reaction = ?", models.PostReactionLike).Count(&likeReactions).Error; err != nil {
		return fmt.Errorf("inspect Post Like reactions: %w", err)
	}
	if nonZeroPostStates > 0 || likeReactions > 0 {
		return fmt.Errorf("Post projection still contains like history (nonzero states=%d reactions=%d); use a separately approved development reset procedure", nonZeroPostStates, likeReactions)
	}
	return nil
}

func initializeUsers(ctx context.Context, db *gorm.DB, store *likes.Store, pageSize int, startAfter uint, stdout io.Writer) (initReport, error) {
	if db == nil || store == nil {
		return initReport{}, errors.New("database and Redis store are required")
	}
	if pageSize < 1 || pageSize > maxUserPageSize {
		return initReport{}, fmt.Errorf("page-size must be between 1 and %d", maxUserPageSize)
	}
	report := initReport{LastUserID: startAfter}
	for {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("interrupted at UserID %d: %w", report.LastUserID, err)
		}
		type userIDRow struct{ ID uint }
		var rows []userIDRow
		if err := db.WithContext(ctx).Unscoped().Model(&models.User{}).
			Select("id").Where("id > ?", report.LastUserID).
			Order("id ASC").Limit(pageSize).Scan(&rows).Error; err != nil {
			return report, fmt.Errorf("read UserID page after %d: %w", report.LastUserID, err)
		}
		if len(rows) == 0 {
			return report, nil
		}
		for _, row := range rows {
			if row.ID == 0 {
				continue
			}
			created, err := store.InitializeUserEmptyWithResult(ctx, row.ID)
			report.LastUserID = row.ID
			if err != nil {
				report.Failed++
				fmt.Fprintf(stdout, "UserID=%d result=failed error=%v\n", row.ID, err)
				continue
			}
			if created {
				report.Initialized++
			} else {
				report.AlreadyReady++
			}
		}
		if _, err := fmt.Fprintf(stdout, "Progress: initialized=%d already_ready=%d failed=%d last_user_id=%d\n",
			report.Initialized, report.AlreadyReady, report.Failed, report.LastUserID); err != nil {
			return report, err
		}
		if len(rows) < pageSize {
			break
		}
	}
	return report, nil
}
