package initialize

import (
	"errors"
	"fmt"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const migrationAdvisoryLockKey int64 = 525716197623

func RunMigrations() error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	return global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationAdvisoryLockKey).Error; err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}

		if err := tx.AutoMigrate(
			&models.User{},
			&models.Article{},
			&models.ArticleBehavior{},
			&models.ArticleReaction{},
			&models.ExchangeRate{},
		); err != nil {
			return fmt.Errorf("auto migrate database: %w", err)
		}

		return nil
	})
}
