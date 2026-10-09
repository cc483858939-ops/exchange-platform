package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/utils"

	"gorm.io/gorm"
)

type provisionReport struct {
	Created  int
	Updated  int
	Restored int
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(stdout io.Writer) error {
	userConfig, err := loadTestUserConfigFromEnvironment()
	if err != nil {
		return err
	}

	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	if global.MaintenanceDb == nil {
		return errors.New("database is not initialized")
	}
	redisClient, err := config.NewRedisClient()
	if err != nil {
		return fmt.Errorf("Redis is required to provision load-test users: %w", err)
	}
	defer redisClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := provisionSyntheticUsers(ctx, global.MaintenanceDb, userConfig, likes.NewStore(redisClient))
	if err != nil {
		return err
	}
	return writeProvisionReport(stdout, userConfig, report)
}

func provisionSyntheticUsers(ctx context.Context, db *gorm.DB, userConfig loadTestUserConfig, initializers ...interface {
	InitializeUserEmpty(context.Context, uint) error
}) (provisionReport, error) {
	if db == nil {
		return provisionReport{}, errors.New("database is required")
	}
	if ctx == nil {
		return provisionReport{}, errors.New("database context is required")
	}
	if userConfig.Password == "" {
		return provisionReport{}, errors.New("LOADTEST_USER_PASSWORD is required")
	}
	if userConfig.Count < minLoadTestUserCount || userConfig.Count > maxLoadTestUserCount {
		return provisionReport{}, fmt.Errorf("load-test user count must be between %d and %d", minLoadTestUserCount, maxLoadTestUserCount)
	}
	if err := validateLoadTestUserPrefix(userConfig.Prefix); err != nil {
		return provisionReport{}, err
	}
	var userLikeInit interface {
		InitializeUserEmpty(context.Context, uint) error
	}
	if len(initializers) > 0 {
		userLikeInit = initializers[0]
	}

	var report provisionReport
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := 1; index <= userConfig.Count; index++ {
			username := syntheticUserUsername(userConfig.Prefix, index)
			displayName := syntheticUserDisplayName(index)

			var user models.User
			findErr := tx.Unscoped().Where("username = ?", username).First(&user).Error
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				hashedPassword, hashErr := utils.HashPassword(userConfig.Password)
				if hashErr != nil {
					return fmt.Errorf("hash password for %s: %w", username, hashErr)
				}
				user = models.User{
					Username:    username,
					Password:    hashedPassword,
					DisplayName: displayName,
				}
				if createErr := tx.Create(&user).Error; createErr != nil {
					return fmt.Errorf("create synthetic user %s: %w", username, createErr)
				}
				if userLikeInit != nil {
					if err := userLikeInit.InitializeUserEmpty(ctx, user.ID); err != nil {
						metrics.RecordLikeLifecycleEvent("user_init_failure")
						return fmt.Errorf("initialize new load-test User %d Like state: %w", user.ID, err)
					}
				}
				report.Created++
				continue
			}
			if findErr != nil {
				return fmt.Errorf("find synthetic user %s: %w", username, findErr)
			}

			hashedPassword, hashErr := utils.HashPassword(userConfig.Password)
			if hashErr != nil {
				return fmt.Errorf("hash password for %s: %w", username, hashErr)
			}
			updates := map[string]interface{}{
				"password":     hashedPassword,
				"display_name": displayName,
				"updated_at":   time.Now().UTC(),
			}
			wasDeleted := user.DeletedAt.Valid
			if wasDeleted {
				updates["deleted_at"] = nil
			}
			if updateErr := tx.Unscoped().Model(&user).Updates(updates).Error; updateErr != nil {
				return fmt.Errorf("update synthetic user %s: %w", username, updateErr)
			}
			if wasDeleted {
				report.Restored++
			} else {
				report.Updated++
			}
		}
		return nil
	})
	if err != nil {
		return provisionReport{}, err
	}
	return report, nil
}

func writeProvisionReport(stdout io.Writer, userConfig loadTestUserConfig, report provisionReport) error {
	_, err := fmt.Fprintf(stdout,
		"Load-test users ready\nprefix=%s\ncount=%d\ncreated=%d\nupdated=%d\nrestored=%d\n",
		userConfig.Prefix, userConfig.Count, report.Created, report.Updated, report.Restored,
	)
	return err
}
