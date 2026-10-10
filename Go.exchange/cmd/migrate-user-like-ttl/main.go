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
	"strings"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

const (
	maxUserLikeMigrationPageSize = 1000
	userLikeTTLModeArm           = "arm"
	userLikeTTLModeRollback      = "rollback"
	userLikeTTLModeAuditOrphan   = "audit-orphan"
)

type migrationOptions struct {
	Mode                        string
	PageSize                    int
	StartAfterUserID            uint
	UserID                      uint
	Apply                       bool
	ConfirmAllAPIsArmingEnabled bool
	ConfirmOldArmingStopped     bool
	ConfirmAllAPIsArmingOff     bool
	ConfirmRegistrationPaused   bool
}

type migrationReport struct {
	Scanned    int64
	Changed    int64
	Preserved  int64
	Anomalies  int64
	LastUserID uint
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	options, err := parseMigrationOptions(args)
	if err != nil {
		return err
	}
	settings, err := config.UserLikeLifecycleSettings()
	if err != nil {
		return err
	}
	if err := validateMigrationSettings(options, settings); err != nil {
		return err
	}

	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	if global.MaintenanceDb == nil {
		return errors.New("maintenance database is not initialized")
	}
	client, err := config.NewRedisClient()
	if err != nil {
		return fmt.Errorf("Redis is required: %w", err)
	}
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store := likes.NewStore(client)
	if options.Mode == userLikeTTLModeAuditOrphan {
		return auditOrphanLedger(ctx, global.MaintenanceDb, client, store, options, stdout)
	}
	report, runErr := migrateUsers(ctx, global.MaintenanceDb, store, options, stdout)
	if _, err := fmt.Fprintf(stdout,
		"Complete: mode=%s dry_run=%t scanned=%d changed=%d preserved=%d anomalies=%d last_user_id=%d\n",
		options.Mode, !options.Apply, report.Scanned, report.Changed, report.Preserved, report.Anomalies, report.LastUserID); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if report.Anomalies > 0 {
		return errors.New("migration completed with User Like state anomalies; inspect output and resume from the last UserID")
	}
	return nil
}

func parseMigrationOptions(args []string) (migrationOptions, error) {
	flags := flag.NewFlagSet("migrate-user-like-ttl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := migrationOptions{}
	flags.StringVar(&options.Mode, "mode", userLikeTTLModeArm, "operation: arm, rollback, or audit-orphan")
	flags.IntVar(&options.PageSize, "page-size", 200, "SQL UserID keyset page size (1-1000)")
	flags.UintVar(&options.StartAfterUserID, "start-after-user-id", 0, "resume SQL UserID keyset after this ID")
	flags.UintVar(&options.UserID, "user-id", 0, "single UserID for --mode=audit-orphan")
	flags.BoolVar(&options.Apply, "apply", false, "apply Redis changes; default is dry-run")
	flags.BoolVar(&options.ConfirmAllAPIsArmingEnabled, "confirm-all-api-instances-arming-enabled", false, "confirm every API instance uses the new TTL arming code and has arming enabled")
	flags.BoolVar(&options.ConfirmOldArmingStopped, "confirm-old-arming-processes-stopped", false, "confirm no old API/worker process can continue arming User Sets")
	flags.BoolVar(&options.ConfirmAllAPIsArmingOff, "confirm-all-api-instances-arming-disabled", false, "confirm all active API instances run with USER_LIKE_TTL_ARMING_ENABLED=false")
	flags.BoolVar(&options.ConfirmRegistrationPaused, "confirm-user-registration-paused", false, "confirm registration is paused while checking and deleting this orphan ledger field")
	if err := flags.Parse(args); err != nil {
		return migrationOptions{}, err
	}
	if flags.NArg() != 0 {
		return migrationOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Mode != userLikeTTLModeArm && options.Mode != userLikeTTLModeRollback && options.Mode != userLikeTTLModeAuditOrphan {
		return migrationOptions{}, errors.New("mode must be arm, rollback, or audit-orphan")
	}
	if options.PageSize < 1 || options.PageSize > maxUserLikeMigrationPageSize {
		return migrationOptions{}, fmt.Errorf("page-size must be between 1 and %d", maxUserLikeMigrationPageSize)
	}
	if options.Mode == userLikeTTLModeAuditOrphan {
		if options.UserID == 0 || options.StartAfterUserID != 0 {
			return migrationOptions{}, errors.New("audit-orphan requires --user-id and does not accept --start-after-user-id")
		}
	} else if options.UserID != 0 {
		return migrationOptions{}, errors.New("--user-id is only valid with --mode=audit-orphan")
	}
	return options, nil
}

func validateMigrationSettings(options migrationOptions, settings config.UserLikeLifecycleConfig) error {
	if !options.Apply {
		return nil
	}
	switch options.Mode {
	case userLikeTTLModeArm:
		if !settings.ArmingEnabled || !options.ConfirmAllAPIsArmingEnabled {
			return errors.New("arm apply requires USER_LIKE_TTL_ARMING_ENABLED=true and --confirm-all-api-instances-arming-enabled")
		}
	case userLikeTTLModeRollback:
		if settings.ArmingEnabled || !options.ConfirmOldArmingStopped || !options.ConfirmAllAPIsArmingOff {
			return errors.New("rollback apply requires arming disabled in this process, --confirm-old-arming-processes-stopped, and --confirm-all-api-instances-arming-disabled")
		}
	case userLikeTTLModeAuditOrphan:
		if !options.ConfirmRegistrationPaused {
			return errors.New("audit-orphan apply requires --confirm-user-registration-paused")
		}
	}
	return nil
}

func migrateUsers(ctx context.Context, db *gorm.DB, store *likes.Store, options migrationOptions, stdout io.Writer) (migrationReport, error) {
	if db == nil || store == nil {
		return migrationReport{}, errors.New("PostgreSQL and Redis are required")
	}
	report := migrationReport{LastUserID: options.StartAfterUserID}
	for {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("interrupted after UserID %d: %w", report.LastUserID, err)
		}
		type userIDRow struct{ ID uint }
		var rows []userIDRow
		if err := db.WithContext(ctx).Model(&models.User{}).
			Select("id").Where("id > ?", report.LastUserID).
			Order("id ASC").Limit(options.PageSize).Scan(&rows).Error; err != nil {
			return report, fmt.Errorf("read UserID page after %d: %w", report.LastUserID, err)
		}
		if len(rows) == 0 {
			return report, nil
		}
		for _, row := range rows {
			if row.ID == 0 {
				continue
			}
			report.Scanned++
			report.LastUserID = row.ID
			state, err := store.InspectUserLikeState(ctx, row.ID)
			if err != nil {
				report.Anomalies++
				if _, writeErr := fmt.Fprintf(stdout, "UserID=%d result=anomaly error=%v\n", row.ID, err); writeErr != nil {
					return report, writeErr
				}
				continue
			}
			if state.Status == "cold" || state.Status == "busy" {
				report.Preserved++
				if _, err := fmt.Fprintf(stdout, "UserID=%d result=preserved_cold\n", row.ID); err != nil {
					return report, err
				}
				continue
			}
			if state.Status != "ready" {
				report.Anomalies++
				if _, err := fmt.Fprintf(stdout, "UserID=%d result=anomaly status=%s\n", row.ID, state.Status); err != nil {
					return report, err
				}
				continue
			}

			action := "would_arm"
			if options.Mode == userLikeTTLModeRollback {
				action = "would_persist"
				if state.TTL < 0 {
					action = "already_persistent"
				}
			} else if state.TTL > 0 && state.ExpiresAt.After(time.Now()) {
				action = "already_armed"
			}
			if options.Apply {
				if options.Mode == userLikeTTLModeArm {
					action, err = store.MigrateUserLikeTTL(ctx, row.ID)
				} else {
					action, err = store.RollbackUserLikeTTL(ctx, row.ID)
				}
				if err != nil {
					report.Anomalies++
					if _, writeErr := fmt.Fprintf(stdout, "UserID=%d result=failed error=%v\n", row.ID, err); writeErr != nil {
						return report, writeErr
					}
					continue
				}
				if action == "armed" || action == "persisted" {
					report.Changed++
				}
			}
			if _, err := fmt.Fprintf(stdout, "UserID=%d result=%s\n", row.ID, action); err != nil {
				return report, err
			}
		}
		if _, err := fmt.Fprintf(stdout, "Progress: mode=%s scanned=%d changed=%d preserved=%d anomalies=%d last_user_id=%d\n",
			options.Mode, report.Scanned, report.Changed, report.Preserved, report.Anomalies, report.LastUserID); err != nil {
			return report, err
		}
		if len(rows) < options.PageSize {
			return report, nil
		}
	}
}

func auditOrphanLedger(ctx context.Context, db *gorm.DB, client *redis.Client, store *likes.Store, options migrationOptions, stdout io.Writer) error {
	if db == nil || client == nil || store == nil || options.UserID == 0 {
		return errors.New("UserID, PostgreSQL, and Redis are required")
	}
	userID := options.UserID
	var count int64
	if err := db.WithContext(ctx).Unscoped().Model(&models.User{}).Where("id = ?", userID).Count(&count).Error; err != nil {
		return fmt.Errorf("check SQL UserID %d: %w", userID, err)
	}
	if count != 0 {
		_, err := fmt.Fprintf(stdout, "UserID=%d result=sql_user_exists; ledger preserved\n", userID)
		return err
	}
	userKeyType, err := client.WithContext(ctx).Type(likes.UserLikesKey(userID)).Result()
	if err != nil {
		return fmt.Errorf("inspect User Like Set type: %w", err)
	}
	if userKeyType != "none" {
		_, err := fmt.Fprintf(stdout, "UserID=%d result=user_set_present; ledger preserved\n", userID)
		return err
	}
	ledgerType, err := client.WithContext(ctx).Type(likes.UserLikesExpiryLedgerKey).Result()
	if err != nil {
		return fmt.Errorf("inspect User Like ledger type: %w", err)
	}
	if ledgerType != "hash" {
		return fmt.Errorf("User Like expiry ledger has type %q; expected hash", ledgerType)
	}
	expiry, err := client.WithContext(ctx).HGet(likes.UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(userID), 10)).Result()
	if err == redis.Nil {
		_, writeErr := fmt.Fprintf(stdout, "UserID=%d result=no_ledger_field\n", userID)
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("read User Like ledger field: %w", err)
	}
	if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(expiry), 10, 64); parseErr != nil || parsed <= 0 {
		return fmt.Errorf("UserID=%d has invalid ledger expiry", userID)
	}
	if !options.Apply {
		_, err := fmt.Fprintf(stdout, "DRY RUN: UserID=%d result=orphan_candidate expiry_unix_ms=%s; no Redis state changed\n", userID, expiry)
		return err
	}
	parsed, _ := strconv.ParseInt(expiry, 10, 64)
	if err := store.CleanupOrphanUserLikeLedger(ctx, userID, parsed); err != nil {
		return fmt.Errorf("clean orphan ledger field for UserID %d: %w", userID, err)
	}
	_, err = fmt.Fprintf(stdout, "UserID=%d result=orphan_ledger_removed\n", userID)
	return err
}
