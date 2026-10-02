package initialize

import (
	"context"
	"errors"
	"fmt"

	"Go.exchange/embeddingstate"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const migrationAdvisoryLockKey int64 = 525716197623

func RunMigrations() error {
	return RunMigrationsWithDB(context.Background(), global.MaintenanceDb)
}

func RunMigrationsWithDB(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		return errors.New("migration context is nil")
	}
	if db == nil {
		return errors.New("database is not initialized")
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationAdvisoryLockKey).Error; err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		previousSchemaVersion, err := readPublishedSchemaVersion(tx)
		if err != nil {
			return fmt.Errorf("read pre-migration schema version: %w", err)
		}

		if err := tx.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
			return fmt.Errorf("enable pgvector extension: %w", err)
		}
		if err := tx.AutoMigrate(
			&models.User{},
			&models.UserFollow{},
			&models.Post{},
			&models.PostMedia{},
			&models.PostMediaUpload{},
			&models.PostRepost{},
			&models.PostBookmark{},
			&models.PostEmbedding{},
			&models.EmbeddingServingState{},
			&models.OutboxEvent{},
			&models.Notification{},
			&models.ConsumerInbox{},
			&models.KafkaDLQReplay{},
			&models.PostBehavior{},
			&models.PostReaction{},
			&models.RecommendationDailyMetric{},
			&models.RecommendationRequest{},
			&models.RecommendationResultTrace{},
			&models.UserPostRecoState{},
			&models.UserRecoProfile{},
			&models.UserAuthorAffinity{},
			&models.UserRecoProfileDirty{},
			&models.ExchangeRate{},
			&models.RuntimeSchemaState{},
			&models.DevDataMirrorAccount{},
			&models.DevDataMirrorPost{},
		); err != nil {
			return fmt.Errorf("auto migrate database: %w", err)
		}
		if previousSchemaVersion < 13 {
			if err := backfillPostQuoteCounts(tx); err != nil {
				return fmt.Errorf("backfill Post quote counts: %w", err)
			}
		}
		if err := applyEmbeddingServingStateSchema(tx); err != nil {
			return err
		}
		if err := applyPostSchemaConstraints(tx); err != nil {
			return err
		}
		if err := applyPostSearchSchema(tx); err != nil {
			return err
		}
		if err := applyPostMediaConstraints(tx); err != nil {
			return err
		}
		if err := applyPostMediaUploadConstraints(tx); err != nil {
			return err
		}
		if err := applyPostEmbeddingConstraints(tx); err != nil {
			return err
		}
		if err := applyUserFollowConstraints(tx); err != nil {
			return err
		}
		if err := applyPostRepostConstraints(tx); err != nil {
			return err
		}
		if err := applyPostBookmarkConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationMetricsConstraints(tx); err != nil {
			return err
		}
		if err := applyPostReactionConstraints(tx); err != nil {
			return err
		}
		if err := applyPostBehaviorConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationTrendingConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationRetrievalV3Indexes(tx); err != nil {
			return err
		}
		if err := applyRecommendationTraceConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationExplorationConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationProfileMaterializationSchema(tx); err != nil {
			return err
		}
		if err := applyRecommendationLanguageAffinityConstraints(tx); err != nil {
			return err
		}
		if err := applyOutboxSchema(tx); err != nil {
			return err
		}
		if err := applyNotificationSchema(tx); err != nil {
			return err
		}
		if err := applyDevDataMirrorConstraints(tx); err != nil {
			return err
		}
		if err := validateMigratedSchema(tx); err != nil {
			return err
		}
		return nil
	})
}

func applyPostSearchSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("database transaction is not initialized")
	}
	statements := []string{
		"CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"CREATE INDEX IF NOT EXISTS idx_posts_search_content_trgm ON posts USING GIN (content gin_trgm_ops) WHERE deleted_at IS NULL AND visibility = 'public'",
		"CREATE INDEX IF NOT EXISTS idx_posts_search_public_created ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply Post search schema: %w", err)
		}
	}
	return nil
}

func readPublishedSchemaVersion(tx *gorm.DB) (int64, error) {
	if tx == nil {
		return 0, errors.New("database transaction is not initialized")
	}
	if !tx.Migrator().HasTable(&models.RuntimeSchemaState{}) {
		return 0, nil
	}
	var state models.RuntimeSchemaState
	err := tx.Select("current_version").Where("id = ?", runtimeSchemaStateID).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return state.CurrentVersion, nil
}

func backfillPostQuoteCounts(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("database transaction is not initialized")
	}
	return tx.Exec(`
WITH active_quote_counts AS (
	SELECT quote_post_id AS target_id, COUNT(*)::bigint AS quote_count
	FROM posts
	WHERE quote_post_id IS NOT NULL
	  AND deleted_at IS NULL
	GROUP BY quote_post_id
)
UPDATE posts AS target
SET quote_count = counts.quote_count
FROM active_quote_counts AS counts
WHERE target.id = counts.target_id`).Error
}

// applyEmbeddingServingStateSchema owns the singleton constraints and seeds
// the initial runtime serving version without overwriting operator changes.
func applyEmbeddingServingStateSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("database transaction is not initialized")
	}
	for _, statement := range []string{
		"ALTER TABLE embedding_serving_state DROP CONSTRAINT IF EXISTS chk_embedding_serving_state_singleton",
		"ALTER TABLE embedding_serving_state ADD CONSTRAINT chk_embedding_serving_state_singleton CHECK (id = 1)",
		"ALTER TABLE embedding_serving_state DROP CONSTRAINT IF EXISTS chk_embedding_serving_state_version_nonblank",
		"ALTER TABLE embedding_serving_state ADD CONSTRAINT chk_embedding_serving_state_version_nonblank CHECK (char_length(btrim(serving_version)) > 0)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply embedding serving state constraints: %w", err)
		}
	}
	if err := tx.Exec(`
INSERT INTO embedding_serving_state (id, serving_version, updated_at)
VALUES (?, ?, NOW())
ON CONFLICT (id) DO NOTHING`, embeddingstate.ServingStateID, embeddingstate.DefaultServingVersion).Error; err != nil {
		return fmt.Errorf("seed embedding serving state: %w", err)
	}
	return nil
}

// applyDevDataMirrorConstraints is deliberately kept out of the runtime
// schema canaries. DevData is an operator/showcase dependency and must not
// make the API or worker readiness contract stricter. Unique indexes are
// owned by the DevData GORM models; this function owns only explicit FKs,
// checks, and non-unique indexes.
func applyDevDataMirrorConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE devdata_mirror_accounts DROP CONSTRAINT IF EXISTS fk_devdata_mirror_accounts_local_user",
		"ALTER TABLE devdata_mirror_accounts ADD CONSTRAINT fk_devdata_mirror_accounts_local_user FOREIGN KEY (local_user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"CREATE INDEX IF NOT EXISTS idx_devdata_mirror_accounts_enabled ON devdata_mirror_accounts (enabled, registry_key)",
		"ALTER TABLE devdata_mirror_posts DROP CONSTRAINT IF EXISTS fk_devdata_mirror_posts_account",
		"ALTER TABLE devdata_mirror_posts ADD CONSTRAINT fk_devdata_mirror_posts_account FOREIGN KEY (mirror_account_id) REFERENCES devdata_mirror_accounts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE devdata_mirror_posts DROP CONSTRAINT IF EXISTS fk_devdata_mirror_posts_post",
		"ALTER TABLE devdata_mirror_posts ADD CONSTRAINT fk_devdata_mirror_posts_post FOREIGN KEY (local_post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE devdata_mirror_posts DROP CONSTRAINT IF EXISTS chk_devdata_mirror_posts_state",
		"ALTER TABLE devdata_mirror_posts ADD CONSTRAINT chk_devdata_mirror_posts_state CHECK (state IN ('active', 'tombstone'))",
		"ALTER TABLE devdata_mirror_posts DROP CONSTRAINT IF EXISTS chk_devdata_mirror_posts_source_metrics",
		"ALTER TABLE devdata_mirror_posts ADD CONSTRAINT chk_devdata_mirror_posts_source_metrics CHECK (source_like_count >= 0 AND source_reply_count >= 0 AND source_repost_count >= 0 AND source_quote_count >= 0)",
		"CREATE INDEX IF NOT EXISTS idx_devdata_mirror_posts_account_state ON devdata_mirror_posts (mirror_account_id, state, source_created_at DESC, id DESC)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply DevData mirror constraints: %w", err)
		}
	}
	return nil
}

func applyPostMediaConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS fk_post_media_post",
		"ALTER TABLE post_media ADD CONSTRAINT fk_post_media_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_type",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_type CHECK (media_type = 'image')",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_position",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_position CHECK (position >= 0 AND position <= 3)",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_url_nonblank",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_url_nonblank CHECK (char_length(trim(url)) > 0)",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_large_url_nonblank",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_large_url_nonblank CHECK (char_length(trim(large_url)) > 0)",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_width_positive",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_width_positive CHECK (width > 0)",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_height_positive",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_height_positive CHECK (height > 0)",
		"ALTER TABLE post_media DROP CONSTRAINT IF EXISTS chk_post_media_medium_dimensions",
		"ALTER TABLE post_media ADD CONSTRAINT chk_post_media_medium_dimensions CHECK (width <= 1200 AND height <= 1200)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_post_media_post_position ON post_media (post_id, position)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post media constraints: %w", err)
		}
	}
	return nil
}

func applyPostMediaUploadConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS fk_post_media_uploads_owner",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT fk_post_media_uploads_owner FOREIGN KEY (owner_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_owner_positive",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_owner_positive CHECK (owner_id > 0)",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_status",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_status CHECK (status IN ('uploading', 'uploaded', 'cleanup_pending'))",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_object_keys_nonblank",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_object_keys_nonblank CHECK (char_length(btrim(original_object_key)) > 0 AND char_length(btrim(medium_object_key)) > 0 AND char_length(btrim(large_object_key)) > 0 AND char_length(btrim(manifest_object_key)) > 0)",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_urls_nonblank",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_urls_nonblank CHECK (char_length(btrim(medium_url)) > 0 AND char_length(btrim(large_url)) > 0)",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_uploaded_shape",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_uploaded_shape CHECK ((status = 'uploading' AND uploaded_at IS NULL) OR (status = 'uploaded' AND uploaded_at IS NOT NULL AND width > 0 AND height > 0 AND cleanup_after >= uploaded_at) OR (status = 'cleanup_pending' AND (uploaded_at IS NULL OR (uploaded_at IS NOT NULL AND width > 0 AND height > 0 AND cleanup_after >= uploaded_at))))",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_cleanup_attempts_nonnegative",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_cleanup_attempts_nonnegative CHECK (cleanup_attempts >= 0)",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_cleanup_claim_shape",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_cleanup_claim_shape CHECK ((status IN ('uploading', 'uploaded') AND cleanup_claim_token IS NULL AND cleanup_claimed_at IS NULL) OR (status = 'cleanup_pending' AND ((cleanup_claim_token IS NULL AND cleanup_claimed_at IS NULL) OR (cleanup_claim_token IS NOT NULL AND cleanup_claimed_at IS NOT NULL))))",
		"ALTER TABLE post_media_uploads DROP CONSTRAINT IF EXISTS chk_post_media_uploads_cleanup_error_size",
		"ALTER TABLE post_media_uploads ADD CONSTRAINT chk_post_media_uploads_cleanup_error_size CHECK (last_cleanup_error IS NULL OR octet_length(last_cleanup_error) <= 1024)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_uploads_status_cleanup_after ON post_media_uploads (status, cleanup_after)",
		"CREATE INDEX IF NOT EXISTS idx_post_media_uploads_gc_eligible ON post_media_uploads (cleanup_after, media_id) WHERE status IN ('uploading', 'uploaded', 'cleanup_pending')",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply Post media upload constraints: %w", err)
		}
	}
	return nil
}

func applyPostSchemaConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_language_supported",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_language_supported CHECK (language IN ('zh', 'ja', 'en', 'und'))",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS fk_posts_author",
		"ALTER TABLE posts ADD CONSTRAINT fk_posts_author FOREIGN KEY (author_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS fk_posts_reply_to_post",
		"ALTER TABLE posts ADD CONSTRAINT fk_posts_reply_to_post FOREIGN KEY (reply_to_post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS fk_posts_quote_post",
		"ALTER TABLE posts ADD CONSTRAINT fk_posts_quote_post FOREIGN KEY (quote_post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS fk_posts_conversation",
		"ALTER TABLE posts ADD CONSTRAINT fk_posts_conversation FOREIGN KEY (conversation_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE RESTRICT",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_visibility_public",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_visibility_public CHECK (visibility = 'public')",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_reply_quote_exclusive",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_reply_quote_exclusive CHECK (NOT (reply_to_post_id IS NOT NULL AND quote_post_id IS NOT NULL))",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_conversation_shape",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_conversation_shape CHECK ((reply_to_post_id IS NULL AND conversation_id IS NULL) OR (reply_to_post_id IS NOT NULL AND conversation_id IS NOT NULL))",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_like_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_like_count_nonnegative CHECK (like_count >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_reply_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_reply_count_nonnegative CHECK (reply_count >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_quote_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_quote_count_nonnegative CHECK (quote_count >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_view_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_view_count_nonnegative CHECK (view_count >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_like_sync_version_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_like_sync_version_nonnegative CHECK (like_sync_version >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_client_publish_identity",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_client_publish_identity CHECK ((client_publish_id IS NULL AND client_publish_fingerprint IS NULL) OR (client_publish_id IS NOT NULL AND client_publish_fingerprint IS NOT NULL AND char_length(client_publish_fingerprint) = 64))",
		"CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts (author_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_reply_to_created ON posts (reply_to_post_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_conversation_created ON posts (conversation_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_quote ON posts (quote_post_id) WHERE quote_post_id IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_deleted_at ON posts (deleted_at)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_posts_author_client_publish_id ON posts (author_id, client_publish_id) WHERE client_publish_id IS NOT NULL",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post schema constraints: %w", err)
		}
	}
	return nil
}

func applyRecommendationProfileMaterializationSchema(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_profile_status",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_profile_status CHECK (profile_status IN ('hit','stale','miss','incompatible'))",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_profile_age",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_profile_age CHECK (profile_age_ms >= 0)",
		"CREATE INDEX IF NOT EXISTS idx_user_post_reco_states_post_user ON user_post_reco_states (post_id, user_id)",
		"CREATE INDEX IF NOT EXISTS idx_user_reco_profile_dirty_due ON user_reco_profile_dirty (next_attempt_at, dirty_at, user_id)",
		"CREATE INDEX IF NOT EXISTS idx_user_reco_profiles_next_rebuild ON user_reco_profiles (next_rebuild_at, user_id)",
		"ALTER TABLE user_post_reco_states DROP CONSTRAINT IF EXISTS fk_user_post_reco_states_user",
		"ALTER TABLE user_post_reco_states ADD CONSTRAINT fk_user_post_reco_states_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_post_reco_states DROP CONSTRAINT IF EXISTS fk_user_post_reco_states_post",
		"ALTER TABLE user_post_reco_states ADD CONSTRAINT fk_user_post_reco_states_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS fk_user_reco_profiles_user",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT fk_user_reco_profiles_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_author_affinities DROP CONSTRAINT IF EXISTS fk_user_author_affinities_user",
		"ALTER TABLE user_author_affinities ADD CONSTRAINT fk_user_author_affinities_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_author_affinities DROP CONSTRAINT IF EXISTS fk_user_author_affinities_author",
		"ALTER TABLE user_author_affinities ADD CONSTRAINT fk_user_author_affinities_author FOREIGN KEY (author_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS fk_user_reco_profile_dirty_user",
		"ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT fk_user_reco_profile_dirty_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_post_reco_states DROP CONSTRAINT IF EXISTS chk_user_post_reco_states_passive_signal",
		"ALTER TABLE user_post_reco_states ADD CONSTRAINT chk_user_post_reco_states_passive_signal CHECK (passive_signal IN ('', 'view', 'click', 'qualified_read', 'neutral_read', 'quick_bounce'))",
		"ALTER TABLE user_post_reco_states DROP CONSTRAINT IF EXISTS chk_user_post_reco_states_negative_signal",
		"ALTER TABLE user_post_reco_states ADD CONSTRAINT chk_user_post_reco_states_negative_signal CHECK (negative_signal IN ('', 'quick_bounce', 'not_interested'))",
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS chk_user_reco_profiles_dimensions",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT chk_user_reco_profiles_dimensions CHECK ((positive_vector IS NULL AND negative_vector IS NULL AND dimensions = 0) OR ((positive_vector IS NOT NULL OR negative_vector IS NOT NULL) AND dimensions > 0 AND (positive_vector IS NULL OR vector_dims(positive_vector) = dimensions) AND (negative_vector IS NULL OR vector_dims(negative_vector) = dimensions)))",
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS chk_user_reco_profiles_counts",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT chk_user_reco_profiles_counts CHECK (negative_evidence >= 0 AND positive_signal_count >= 0 AND negative_signal_count >= 0 AND personalized_signal_count >= 0)",
		"ALTER TABLE user_author_affinities DROP CONSTRAINT IF EXISTS chk_user_author_affinities_raw_nonnegative",
		"ALTER TABLE user_author_affinities ADD CONSTRAINT chk_user_author_affinities_raw_nonnegative CHECK (raw_affinity >= 0)",
		"ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS chk_user_reco_profile_dirty_reason",
		"ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT chk_user_reco_profile_dirty_reason CHECK (char_length(reason) <= 64)",
		"ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS chk_user_reco_profile_dirty_error",
		"ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT chk_user_reco_profile_dirty_error CHECK (char_length(last_error) <= 512)",
		"ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS chk_user_reco_profile_dirty_version",
		"ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT chk_user_reco_profile_dirty_version CHECK (dirty_version >= 1 AND attempts >= 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation profile materialization schema: %w", err)
		}
	}
	return nil
}

func applyPostEmbeddingConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_embeddings DROP CONSTRAINT IF EXISTS post_embeddings_pkey",
		"ALTER TABLE post_embeddings ADD CONSTRAINT post_embeddings_pkey PRIMARY KEY (post_id, version)",
		"CREATE INDEX IF NOT EXISTS idx_post_embeddings_version_post ON post_embeddings (version, post_id)",
		"ALTER TABLE post_embeddings DROP CONSTRAINT IF EXISTS fk_post_embeddings_post",
		"ALTER TABLE post_embeddings ADD CONSTRAINT fk_post_embeddings_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_embeddings DROP CONSTRAINT IF EXISTS chk_post_embeddings_vector_dimensions",
		"ALTER TABLE post_embeddings ADD CONSTRAINT chk_post_embeddings_vector_dimensions CHECK (vector_dims(embedding) = dimensions)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post embedding constraint: %w", err)
		}
	}
	return nil
}

func applyUserFollowConstraints(tx *gorm.DB) error {
	statements := []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_user_follows_pair ON user_follows (follower_id, following_id)",
		"ALTER TABLE user_follows DROP CONSTRAINT IF EXISTS fk_user_follows_follower",
		"ALTER TABLE user_follows ADD CONSTRAINT fk_user_follows_follower FOREIGN KEY (follower_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_follows DROP CONSTRAINT IF EXISTS fk_user_follows_following",
		"ALTER TABLE user_follows ADD CONSTRAINT fk_user_follows_following FOREIGN KEY (following_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_follows DROP CONSTRAINT IF EXISTS chk_user_follows_not_self",
		"ALTER TABLE user_follows ADD CONSTRAINT chk_user_follows_not_self CHECK (follower_id <> following_id)",
		"CREATE INDEX IF NOT EXISTS idx_user_follows_follower_created ON user_follows (follower_id, created_at DESC, id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_user_follows_following_created ON user_follows (following_id, created_at DESC, id DESC)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply user follow constraint: %w", err)
		}
	}
	return nil
}

func applyPostRepostConstraints(tx *gorm.DB) error {
	statements := []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_post_reposts_user_post ON post_reposts (user_id, post_id)",
		"CREATE INDEX IF NOT EXISTS idx_post_reposts_user_created ON post_reposts (user_id, created_at DESC, id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_post_reposts_post ON post_reposts (post_id)",
		"ALTER TABLE post_reposts DROP CONSTRAINT IF EXISTS fk_post_reposts_user",
		"ALTER TABLE post_reposts ADD CONSTRAINT fk_post_reposts_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_reposts DROP CONSTRAINT IF EXISTS fk_post_reposts_post",
		"ALTER TABLE post_reposts ADD CONSTRAINT fk_post_reposts_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post repost constraint: %w", err)
		}
	}
	return nil
}

func applyPostBookmarkConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_bookmarks DROP CONSTRAINT IF EXISTS fk_post_bookmarks_user",
		"ALTER TABLE post_bookmarks ADD CONSTRAINT fk_post_bookmarks_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_bookmarks DROP CONSTRAINT IF EXISTS fk_post_bookmarks_post",
		"ALTER TABLE post_bookmarks ADD CONSTRAINT fk_post_bookmarks_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"CREATE INDEX IF NOT EXISTS idx_post_bookmarks_user_created ON post_bookmarks (user_id, created_at DESC, post_id ASC)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post bookmark constraint: %w", err)
		}
	}
	return nil
}

func applyPostReactionConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_reaction DROP CONSTRAINT IF EXISTS fk_post_reaction_user",
		"ALTER TABLE post_reaction ADD CONSTRAINT fk_post_reaction_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_reaction DROP CONSTRAINT IF EXISTS fk_post_reaction_post",
		"ALTER TABLE post_reaction ADD CONSTRAINT fk_post_reaction_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_reaction DROP CONSTRAINT IF EXISTS chk_post_reaction_version_positive",
		"ALTER TABLE post_reaction ADD CONSTRAINT chk_post_reaction_version_positive CHECK (reaction_version > 0)",
		"CREATE INDEX IF NOT EXISTS idx_post_reaction_user_liked_state ON post_reaction (user_id, liked, state_changed_at DESC, post_id)",
		"CREATE INDEX IF NOT EXISTS idx_post_behavior_user_view_seen ON post_behaviors (user_id, action, last_seen_at DESC, id DESC) WHERE action = 'view'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post reaction constraints: %w", err)
		}
	}
	return nil
}

func applyPostBehaviorConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_behaviors DROP CONSTRAINT IF EXISTS fk_post_behaviors_user",
		"ALTER TABLE post_behaviors ADD CONSTRAINT fk_post_behaviors_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_behaviors DROP CONSTRAINT IF EXISTS fk_post_behaviors_post",
		"ALTER TABLE post_behaviors ADD CONSTRAINT fk_post_behaviors_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post behavior constraint: %w", err)
		}
	}
	return nil
}

func applyRecommendationTrendingConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_trending_candidates",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_trending_candidates CHECK (trending_candidate_count >= 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation trending constraints: %w", err)
		}
	}
	return nil
}

func applyRecommendationRetrievalV3Indexes(tx *gorm.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_posts_recommendation_recent ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public' AND reply_to_post_id IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_recommendation_trending ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public' AND reply_to_post_id IS NULL AND (like_count > 0 OR reply_count > 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation retrieval v3 index: %w", err)
		}
	}
	return nil
}
func applyRecommendationMetricsConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS chk_recommendation_metric_feed_dwell_count",
		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS chk_recommendation_metric_feed_visible_time",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT chk_recommendation_metric_feed_dwell_count CHECK (feed_dwell_count >= 0)",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT chk_recommendation_metric_feed_visible_time CHECK (feed_visible_time_ms >= 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation metrics constraint: %w", err)
		}
	}
	return nil
}
func applyRecommendationTraceConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_fallback",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_fallback CHECK (fallback_reason IN ('', 'no_positive_profile', 'insufficient_fresh_candidates'))",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_personalization_mode",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_personalization_mode CHECK (personalization_mode IN ('semantic_social', 'social_only', 'cold_start'))",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_recommendation_result_trace_request_post ON recommendation_result_traces (request_id, post_id)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_post ON recommendation_result_traces (post_id)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_created ON recommendation_result_traces (created_at)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_expires ON recommendation_result_traces (expires_at)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS fk_recommendation_result_traces_request",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT fk_recommendation_result_traces_request FOREIGN KEY (request_id) REFERENCES recommendation_requests(request_id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS fk_recommendation_result_traces_post",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT fk_recommendation_result_traces_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation trace constraint: %w", err)
		}
	}
	return nil
}

func applyRecommendationExplorationConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_target",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_target CHECK (exploration_target_count >= 0 AND exploration_target_count <= requested_limit)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_opportunity",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_opportunity CHECK (exploration_opportunity_count >= 0 AND exploration_opportunity_count <= exploration_target_count)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_result",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_result CHECK (exploration_result_count >= 0 AND exploration_result_count <= exploration_opportunity_count AND exploration_result_count <= result_count)",

		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_selection_mode",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_selection_mode CHECK (selection_mode IN ('ranked', 'exploration'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_exploration_reason",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_exploration_reason CHECK (exploration_reason IN ('', 'recent', 'novel_author', 'recent_novel_author'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_exploration_semantic",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_exploration_semantic CHECK (exploration_semantic >= 0 AND exploration_semantic <= 1)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_provenance",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_provenance CHECK ((selection_mode = 'ranked' AND exploration_reason = '' AND exploration_semantic = 0) OR (exploration_opportunity AND selection_mode = 'exploration' AND exploration_reason IN ('recent', 'novel_author', 'recent_novel_author')))",

		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS chk_recommendation_metric_selection_mode",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT chk_recommendation_metric_selection_mode CHECK (selection_mode IN ('ranked', 'exploration'))",
		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS chk_recommendation_metric_exploration_reason",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT chk_recommendation_metric_exploration_reason CHECK (exploration_reason IN ('', 'recent', 'novel_author', 'recent_novel_author'))",
		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS chk_recommendation_metric_provenance",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT chk_recommendation_metric_provenance CHECK ((selection_mode = 'ranked' AND exploration_reason = '') OR (exploration_opportunity AND selection_mode = 'exploration' AND exploration_reason IN ('recent', 'novel_author', 'recent_novel_author')))",
		"ALTER TABLE recommendation_daily_metrics DROP CONSTRAINT IF EXISTS recommendation_daily_metrics_pkey",
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT recommendation_daily_metrics_pkey PRIMARY KEY (metric_date, scene, ranker_version, ranker_config_hash, strategy_id, exploration_opportunity, selection_mode, exploration_reason, position, post_id)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation exploration constraints: %w", err)
		}
	}
	return nil
}
