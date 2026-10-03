package initialize

import (
	"fmt"

	"gorm.io/gorm"
)

func applyPostMediaDeletionSchema(tx *gorm.DB, previousVersion int64) error {
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS idx_post_media_url ON post_media (url)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_large_url ON post_media (large_url)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_cleanup_due ON post_media_cleanup (cleanup_after, media_id)",
		"ALTER TABLE post_media_cleanup DROP CONSTRAINT IF EXISTS chk_post_media_cleanup_identity",
		"ALTER TABLE post_media_cleanup ADD CONSTRAINT chk_post_media_cleanup_identity CHECK (owner_id > 0 AND char_length(btrim(medium_object_key)) > 0 AND char_length(btrim(large_object_key)) > 0 AND attempts >= 0)",
		"ALTER TABLE post_media_cleanup DROP CONSTRAINT IF EXISTS chk_post_media_cleanup_error",
		"ALTER TABLE post_media_cleanup ADD CONSTRAINT chk_post_media_cleanup_error CHECK (last_error IS NULL OR octet_length(last_error) <= 1024)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply Post media deletion schema: %w", err)
		}
	}
	if previousVersion >= 15 {
		return nil
	}
	// Old deleted Posts also lose file eligibility immediately. Register their
	// canonical user upload folders once so storage can catch up after rollout.
	return tx.Exec(`INSERT INTO post_media_cleanup
(media_id, owner_id, medium_object_key, large_object_key, created_at, cleanup_after, attempts)
SELECT DISTINCT ON (split_part(m.url, '/', 8))
split_part(m.url, '/', 8)::uuid, p.author_id, substring(m.url from 12), substring(m.large_url from 12), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 0
FROM post_media m JOIN posts p ON p.id = m.post_id
WHERE p.deleted_at IS NOT NULL
AND m.url ~ '^/api/files/post-media/users/v1/[1-9][0-9]*/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/medium[.](jpg|png)$'
AND split_part(m.url, '/', 7) = p.author_id::text
AND m.large_url IN (
regexp_replace(m.url, 'medium[.](jpg|png)$', 'large.jpg'),
regexp_replace(m.url, 'medium[.](jpg|png)$', 'large.png'))
ORDER BY split_part(m.url, '/', 8), m.id
ON CONFLICT (media_id) DO NOTHING`).Error
}
