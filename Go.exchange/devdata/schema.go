package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"Go.exchange/models"

	"gorm.io/gorm"
)

var requiredMetadataConstraints = map[string][]string{
	"devdata_mirror_accounts": {
		"ucon_devdata_mirror_accounts_registry_key",
		"ucon_devdata_mirror_accounts_platform_source_user",
		"ucon_devdata_mirror_accounts_local_user",
		"fk_devdata_mirror_accounts_local_user",
	},
	"devdata_mirror_posts": {
		"ucon_devdata_mirror_posts_platform_source_post",
		"ucon_devdata_mirror_posts_local_post",
		"fk_devdata_mirror_posts_account",
		"fk_devdata_mirror_posts_post",
		"chk_devdata_mirror_posts_state",
		"chk_devdata_mirror_posts_source_metrics",
	},
}

var requiredMetadataIndexes = map[string][]string{
	"devdata_mirror_accounts": {"idx_devdata_mirror_accounts_enabled"},
	"devdata_mirror_posts":    {"idx_devdata_mirror_posts_account_state"},
}

// ValidateMetadataSchema is the DevData-only readiness check. It intentionally
// does not call initialize.CheckRuntimeSchema and does not add the metadata
// tables to API/worker runtime readiness.
func ValidateMetadataSchema(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return errors.New("database is not initialized")
	}
	for table := range requiredMetadataConstraints {
		var exists bool
		if err := db.WithContext(ctx).Raw(`
SELECT EXISTS (
  SELECT 1
  FROM pg_class AS class
  JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
  WHERE class.relname = ?
    AND class.relkind IN ('r', 'p')
    AND namespace.nspname = ANY(current_schemas(false))
)`, table).Scan(&exists).Error; err != nil {
			return fmt.Errorf("check DevData table %s: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("DevData metadata table is missing: %s", table)
		}
	}

	for table, names := range requiredMetadataConstraints {
		var rows []struct {
			Name string `gorm:"column:conname"`
		}
		if err := db.WithContext(ctx).Raw(`
SELECT constraints_catalog.conname
FROM pg_constraint AS constraints_catalog
JOIN pg_class AS class ON class.oid = constraints_catalog.conrelid
JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
WHERE class.relname = ?
  AND namespace.nspname = ANY(current_schemas(false))
`, table).Scan(&rows).Error; err != nil {
			return fmt.Errorf("check DevData constraints for %s: %w", table, err)
		}
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Name] = struct{}{}
		}
		for _, name := range names {
			if _, ok := seen[name]; !ok {
				return fmt.Errorf("DevData metadata constraint is missing: %s", name)
			}
		}
	}

	for table, names := range requiredMetadataIndexes {
		var rows []struct {
			Name string `gorm:"column:indexname"`
		}
		if err := db.WithContext(ctx).Raw(`
SELECT indexname
FROM pg_indexes
WHERE tablename = ?
  AND schemaname = ANY(current_schemas(false))
`, table).Scan(&rows).Error; err != nil {
			return fmt.Errorf("check DevData indexes for %s: %w", table, err)
		}
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Name] = struct{}{}
		}
		for _, name := range names {
			if _, ok := seen[name]; !ok {
				return fmt.Errorf("DevData metadata index is missing: %s", name)
			}
		}
	}
	return nil
}

func validateMetadataModelTables(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if !db.Migrator().HasTable(&models.DevDataMirrorAccount{}) || !db.Migrator().HasTable(&models.DevDataMirrorPost{}) {
		return errors.New("DevData metadata tables are missing")
	}
	return nil
}

func metadataSourceKey(platform, sourceID string) string {
	return strings.ToLower(strings.TrimSpace(platform)) + "\x00" + strings.TrimSpace(sourceID)
}
