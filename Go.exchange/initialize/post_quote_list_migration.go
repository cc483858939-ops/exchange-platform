package initialize

import (
	"fmt"

	"gorm.io/gorm"
)

// Keep this performance-only index in its own final migration step so adding
// it does not change the checksum of the existing Post constraint migration.
func applyPostQuoteListIndex(tx *gorm.DB) error {
	if err := applyMigrationStatements(tx, "index public Post quote pagination", []string{
		"CREATE INDEX IF NOT EXISTS idx_posts_quotes_public_created ON posts (quote_post_id, created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public' AND quote_post_id IS NOT NULL",
	}); err != nil {
		return fmt.Errorf("index public Post quote pagination: %w", err)
	}
	return nil
}
