package initialize

import (
	"errors"
	"fmt"
	"strings"

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

		if err := prepareLegacyOutboxSchema(tx); err != nil {
			return err
		}
		if err := tx.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
			return fmt.Errorf("enable pgvector extension: %w", err)
		}
		if err := prepareDevDataMirrorUniqueIndexes(tx); err != nil {
			return err
		}

		if err := tx.AutoMigrate(
			&models.User{},
			&models.UserFollow{},
			&models.Post{},
			&models.PostArticle{},
			&models.PostRepost{},
			&models.PostEmbedding{},
			&models.OutboxEvent{},
			&models.Notification{},
			&models.ConsumerInbox{},
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

		if err := applyLegacyPostEmbeddingJobCleanup(tx); err != nil {
			return err
		}
		if err := applyPostSchemaConstraints(tx); err != nil {
			return err
		}
		if err := applyPostArticleConstraints(tx); err != nil {
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
		if err := applyRecommendationMetricsConstraints(tx); err != nil {
			return err
		}
		if err := applyPostReactionConstraints(tx); err != nil {
			return err
		}
		if err := applyPostBehaviorConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationTrendingSchemaCleanup(tx); err != nil {
			return err
		}
		if err := applyRecommendationRetrievalV3Indexes(tx); err != nil {
			return err
		}
		if err := applyRecommendationTraceConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationExplorationSchema(tx); err != nil {
			return err
		}
		if err := applyRecommendationProfileMaterializationSchema(tx); err != nil {
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
		if err := tx.Exec(`
UPDATE post_reaction
SET liked = (reaction = 1)
WHERE reaction_version = 0
`).Error; err != nil {
			return fmt.Errorf("backfill post reaction tombstones: %w", err)
		}
		if err := validateMigratedSchema(tx); err != nil {
			return err
		}
		return nil
	})
}

// prepareDevDataMirrorUniqueIndexes transfers the old explicitly-created
// single/composite UNIQUE constraints to the GORM-owned unique indexes below.
//
// GORM's PostgreSQL AutoMigrate inspects single-column UNIQUE constraints. If
// it sees one that is not represented by a `unique` field tag, it tries to
// drop the conventionally named `uni_<table>_<column>` constraint. The old
// DevData migration used different explicit names, so the second migration
// could fail while trying to drop a constraint that never existed. Removing
// the old explicit constraints before AutoMigrate makes the transfer
// transactional and lets the model's exact unique-index names be the sole
// owner on fresh and existing databases. Composite constraints are included
// as well so no old ownership variant survives the transfer.
func prepareDevDataMirrorUniqueIndexes(tx *gorm.DB) error {
	for _, model := range []interface{}{
		&models.DevDataMirrorAccount{},
		&models.DevDataMirrorPost{},
	} {
		if !tx.Migrator().HasTable(model) {
			continue
		}
		table := "devdata_mirror_accounts"
		if _, ok := model.(*models.DevDataMirrorPost); ok {
			table = "devdata_mirror_posts"
		}
		for _, constraint := range []string{
			"ucon_devdata_mirror_accounts_registry_key",
			"ucon_devdata_mirror_accounts_platform_source_user",
			"ucon_devdata_mirror_accounts_local_user",
			"ucon_devdata_mirror_posts_platform_source_post",
			"ucon_devdata_mirror_posts_local_post",
		} {
			if strings.HasPrefix(constraint, "ucon_devdata_mirror_accounts_") && table != "devdata_mirror_accounts" {
				continue
			}
			if strings.HasPrefix(constraint, "ucon_devdata_mirror_posts_") && table != "devdata_mirror_posts" {
				continue
			}
			if err := tx.Exec("ALTER TABLE " + table + " DROP CONSTRAINT IF EXISTS " + constraint).Error; err != nil {
				return fmt.Errorf("prepare DevData unique index ownership for %s: %w", table, err)
			}
		}
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

// applyLegacyPostEmbeddingJobCleanup removes the obsolete one-off work table.
// It does not migrate or rewrite any content rows.
func applyLegacyPostEmbeddingJobCleanup(tx *gorm.DB) error {
	if err := tx.Exec("DROP TABLE IF EXISTS article_embedding_jobs").Error; err != nil {
		return fmt.Errorf("drop legacy post embedding jobs: %w", err)
	}
	return nil
}

func applyPostSchemaConstraints(tx *gorm.DB) error {
	statements := []string{
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
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_view_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_view_count_nonnegative CHECK (view_count >= 0)",
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_like_sync_version_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_like_sync_version_nonnegative CHECK (like_sync_version >= 0)",
		"CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts (author_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_reply_to_created ON posts (reply_to_post_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_conversation_created ON posts (conversation_id, created_at DESC, id DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_quote ON posts (quote_post_id) WHERE quote_post_id IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_deleted_at ON posts (deleted_at)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post schema constraints: %w", err)
		}
	}
	return nil
}

func applyPostArticleConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_articles ALTER COLUMN post_id SET NOT NULL",
		"ALTER TABLE post_articles ALTER COLUMN title SET NOT NULL",
		"ALTER TABLE post_articles ALTER COLUMN preview SET NOT NULL",
		"ALTER TABLE post_articles ALTER COLUMN cover_image_url SET NOT NULL",
		"ALTER TABLE post_articles ALTER COLUMN publication_state SET NOT NULL",
		"ALTER TABLE post_articles ALTER COLUMN published_at SET NOT NULL",
		"ALTER TABLE post_articles DROP CONSTRAINT IF EXISTS fk_post_articles_post",
		"ALTER TABLE post_articles ADD CONSTRAINT fk_post_articles_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_articles DROP CONSTRAINT IF EXISTS chk_post_articles_publication_state",
		"ALTER TABLE post_articles ADD CONSTRAINT chk_post_articles_publication_state CHECK (publication_state = 'published')",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post article constraints: %w", err)
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

func applyPostReactionConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE post_reaction DROP CONSTRAINT IF EXISTS fk_post_reaction_user",
		"ALTER TABLE post_reaction ADD CONSTRAINT fk_post_reaction_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_reaction DROP CONSTRAINT IF EXISTS fk_post_reaction_post",
		"ALTER TABLE post_reaction ADD CONSTRAINT fk_post_reaction_post FOREIGN KEY (post_id) REFERENCES posts(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE post_reaction ALTER COLUMN liked DROP DEFAULT",
		"ALTER TABLE post_reaction ADD COLUMN IF NOT EXISTS state_changed_at TIMESTAMPTZ",
		"UPDATE post_reaction SET state_changed_at = COALESCE(state_changed_at, updated_at, CURRENT_TIMESTAMP)",
		"ALTER TABLE post_reaction ALTER COLUMN state_changed_at SET NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_post_reaction_user_liked_state ON post_reaction (user_id, liked, state_changed_at DESC, post_id)",
		"CREATE INDEX IF NOT EXISTS idx_post_behavior_user_view_seen ON post_behaviors (user_id, action, last_seen_at DESC, id DESC) WHERE action = 'view'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply post reaction constraint: %w", err)
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

func applyRecommendationTrendingSchemaCleanup(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_popular_candidates",
		"ALTER TABLE recommendation_requests DROP COLUMN IF EXISTS popular_candidate_count",
		"ALTER TABLE recommendation_result_traces DROP COLUMN IF EXISTS from_popular",
		"ALTER TABLE recommendation_result_traces DROP COLUMN IF EXISTS popularity_component",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_trending_candidates",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_trending_candidates CHECK (trending_candidate_count >= 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation trending schema cleanup: %w", err)
		}
	}
	return nil
}

func applyRecommendationRetrievalV3Indexes(tx *gorm.DB) error {
	statements := []string{
		"DROP INDEX IF EXISTS idx_posts_recommendation_popular",
		"CREATE INDEX IF NOT EXISTS idx_posts_recommendation_recent ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public' AND reply_to_post_id IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_posts_recommendation_trending ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public' AND reply_to_post_id IS NULL AND (like_count > 0 OR reply_count > 0)",
		"CREATE INDEX IF NOT EXISTS idx_post_articles_recommendation_published ON post_articles (published_at DESC, post_id) WHERE publication_state = 'published' AND published_at IS NOT NULL",
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
		"ALTER TABLE recommendation_daily_metrics ADD COLUMN IF NOT EXISTS feed_dwell_count BIGINT",
		"ALTER TABLE recommendation_daily_metrics ADD COLUMN IF NOT EXISTS feed_visible_time_ms BIGINT",
		"UPDATE recommendation_daily_metrics SET feed_dwell_count = 0 WHERE feed_dwell_count IS NULL",
		"UPDATE recommendation_daily_metrics SET feed_visible_time_ms = 0 WHERE feed_visible_time_ms IS NULL",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN feed_dwell_count SET DEFAULT 0",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN feed_visible_time_ms SET DEFAULT 0",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN feed_dwell_count SET NOT NULL",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN feed_visible_time_ms SET NOT NULL",
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
		"DROP INDEX IF EXISTS uidx_recommendation_result_trace_request_article",
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

func applyRecommendationExplorationSchema(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS exploration_target_count INTEGER",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS exploration_opportunity_count INTEGER",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS exploration_result_count INTEGER",
		"UPDATE recommendation_requests SET exploration_target_count = COALESCE(exploration_target_count, 0), exploration_opportunity_count = COALESCE(exploration_opportunity_count, 0), exploration_result_count = COALESCE(exploration_result_count, 0)",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_target_count SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_opportunity_count SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_result_count SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_target_count SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_opportunity_count SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN exploration_result_count SET NOT NULL",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_target",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_target CHECK (exploration_target_count >= 0 AND exploration_target_count <= requested_limit)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_opportunity",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_opportunity CHECK (exploration_opportunity_count >= 0 AND exploration_opportunity_count <= exploration_target_count)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_exploration_result",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_exploration_result CHECK (exploration_result_count >= 0 AND exploration_result_count <= exploration_opportunity_count AND exploration_result_count <= result_count)",

		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS exploration_opportunity BOOLEAN",
		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS selection_mode VARCHAR(16)",
		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS exploration_reason VARCHAR(32)",
		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS exploration_semantic DOUBLE PRECISION",
		"UPDATE recommendation_result_traces SET exploration_opportunity = COALESCE(exploration_opportunity, FALSE), selection_mode = COALESCE(NULLIF(selection_mode, ''), 'ranked'), exploration_reason = COALESCE(exploration_reason, ''), exploration_semantic = COALESCE(exploration_semantic, 0)",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_opportunity SET DEFAULT FALSE",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN selection_mode SET DEFAULT 'ranked'",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_reason SET DEFAULT ''",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_semantic SET DEFAULT 0",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_opportunity SET NOT NULL",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN selection_mode SET NOT NULL",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_reason SET NOT NULL",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN exploration_semantic SET NOT NULL",
		"ALTER TABLE recommendation_result_traces DROP COLUMN IF EXISTS freshness_component",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_selection_mode",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_selection_mode CHECK (selection_mode IN ('ranked', 'exploration'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_exploration_reason",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_exploration_reason CHECK (exploration_reason IN ('', 'recent', 'novel_author', 'recent_novel_author'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_exploration_semantic",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_exploration_semantic CHECK (exploration_semantic >= 0 AND exploration_semantic <= 1)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_provenance",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_provenance CHECK ((selection_mode = 'ranked' AND exploration_reason = '' AND exploration_semantic = 0) OR (exploration_opportunity AND selection_mode = 'exploration' AND exploration_reason IN ('recent', 'novel_author', 'recent_novel_author')))",

		"ALTER TABLE recommendation_daily_metrics ADD COLUMN IF NOT EXISTS exploration_opportunity BOOLEAN",
		"ALTER TABLE recommendation_daily_metrics ADD COLUMN IF NOT EXISTS selection_mode VARCHAR(16)",
		"ALTER TABLE recommendation_daily_metrics ADD COLUMN IF NOT EXISTS exploration_reason VARCHAR(32)",
		"UPDATE recommendation_daily_metrics SET exploration_opportunity = COALESCE(exploration_opportunity, FALSE), selection_mode = COALESCE(NULLIF(selection_mode, ''), 'ranked'), exploration_reason = COALESCE(exploration_reason, '')",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN exploration_opportunity SET DEFAULT FALSE",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN selection_mode SET DEFAULT 'ranked'",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN exploration_reason SET DEFAULT ''",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN exploration_opportunity SET NOT NULL",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN selection_mode SET NOT NULL",
		"ALTER TABLE recommendation_daily_metrics ALTER COLUMN exploration_reason SET NOT NULL",
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
			return fmt.Errorf("apply recommendation exploration schema: %w", err)
		}
	}
	return nil
}
