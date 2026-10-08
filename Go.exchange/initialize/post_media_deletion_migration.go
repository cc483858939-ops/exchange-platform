package initialize

import (
	"fmt"

	"gorm.io/gorm"
)

func applyPostMediaDeletionSchema(tx *gorm.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_post_media_url ON post_media (url)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_large_url ON post_media (large_url)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_cleanup_due ON post_media_cleanup (cleanup_after, media_id)",
		"ALTER TABLE post_media_cleanup DROP CONSTRAINT IF EXISTS chk_post_media_cleanup_identity",
		"ALTER TABLE post_media_cleanup ADD CONSTRAINT chk_post_media_cleanup_identity CHECK (owner_id > 0 AND char_length(btrim(medium_object_key)) > 0 AND char_length(btrim(large_object_key)) > 0 AND attempts >= 0)",
		"ALTER TABLE post_media_cleanup DROP CONSTRAINT IF EXISTS chk_post_media_cleanup_error",
		"ALTER TABLE post_media_cleanup ADD CONSTRAINT chk_post_media_cleanup_error CHECK (last_error IS NULL OR octet_length(last_error) <= 1024)",
	}
	if err := applyMigrationStatements(tx, "apply Post media deletion schema", statements); err != nil {
		return fmt.Errorf("apply Post media deletion schema: %w", err)
	}
	return nil
}
