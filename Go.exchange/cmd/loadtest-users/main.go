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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := provisionSyntheticUsers(ctx, global.MaintenanceDb, userConfig)
	if err != nil {
		return err
	}
	return writeProvisionReport(stdout, userConfig, report)
}

func provisionSyntheticUsers(ctx context.Context, db *gorm.DB, userConfig loadTestUserConfig) (provisionReport, error) {
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
				if createErr := tx.Create(&models.User{
					Username:    username,
					Password:    hashedPassword,
					DisplayName: displayName,
				}).Error; createErr != nil {
					return fmt.Errorf("create synthetic user %s: %w", username, createErr)
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
