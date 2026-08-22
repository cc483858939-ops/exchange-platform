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

		if err := tx.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
			return fmt.Errorf("enable pgvector extension: %w", err)
		}
		if err := prepareLegacyOutboxSchema(tx); err != nil {
			return err
		}

		if err := tx.AutoMigrate(
			&models.User{},
			&models.UserFollow{},
			&models.Article{},
			&models.Comment{},
			&models.ArticleEmbedding{},
			&models.OutboxEvent{},
			&models.Notification{},
			&models.ConsumerInbox{},
			&models.ArticleBehavior{},
			&models.ArticleReaction{},
			&models.RecommendationDailyMetric{},
			&models.RecommendationRequest{},
			&models.RecommendationResultTrace{},
			&models.UserArticleRecoState{},
			&models.UserRecoProfile{},
			&models.UserAuthorAffinity{},
			&models.UserRecoProfileDirty{},
			&models.ExchangeRate{},
		); err != nil {
			return fmt.Errorf("auto migrate database: %w", err)
		}

		if err := applyLegacyAISchemaCleanup(tx); err != nil {
			return err
		}
		if err := applyLegacyArticleEmbeddingJobCleanup(tx); err != nil {
			return err
		}
		if err := applyArticleEmbeddingConstraints(tx); err != nil {
			return err
		}
		if err := applyUserFollowConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationMetricsConstraints(tx); err != nil {
			return err
		}
		if err := applyArticleReactionConstraints(tx); err != nil {
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
		if err := applyArticleAuthorConstraints(tx); err != nil {
			return err
		}
		if err := applyArticleEngagementConstraints(tx); err != nil {
			return err
		}
		if err := applyCommentConstraints(tx); err != nil {
			return err
		}
		if err := applyOutboxSchema(tx); err != nil {
			return err
		}
		if err := applyNotificationSchema(tx); err != nil {
			return err
		}
		if err := tx.Exec(`
UPDATE article_reaction
SET liked = (reaction = 1)
WHERE reaction_version = 0
`).Error; err != nil {
			return fmt.Errorf("backfill article reaction tombstones: %w", err)
		}
		return nil
	})
}

func applyRecommendationProfileMaterializationSchema(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_profile_status",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_profile_status CHECK (profile_status IN ('hit','stale','miss','incompatible'))",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_profile_age",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_profile_age CHECK (profile_age_ms >= 0)",
		"CREATE INDEX IF NOT EXISTS idx_user_article_reco_states_article_user ON user_article_reco_states (article_id, user_id)",
		"CREATE INDEX IF NOT EXISTS idx_user_reco_profile_dirty_due ON user_reco_profile_dirty (next_attempt_at, dirty_at, user_id)",
		"CREATE INDEX IF NOT EXISTS idx_user_reco_profiles_next_rebuild ON user_reco_profiles (next_rebuild_at, user_id)",
		"ALTER TABLE user_article_reco_states DROP CONSTRAINT IF EXISTS fk_user_article_reco_states_user",
		"ALTER TABLE user_article_reco_states ADD CONSTRAINT fk_user_article_reco_states_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_article_reco_states DROP CONSTRAINT IF EXISTS fk_user_article_reco_states_article",
		"ALTER TABLE user_article_reco_states ADD CONSTRAINT fk_user_article_reco_states_article FOREIGN KEY (article_id) REFERENCES articles(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS fk_user_reco_profiles_user",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT fk_user_reco_profiles_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_author_affinities DROP CONSTRAINT IF EXISTS fk_user_author_affinities_user",
		"ALTER TABLE user_author_affinities ADD CONSTRAINT fk_user_author_affinities_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_author_affinities DROP CONSTRAINT IF EXISTS fk_user_author_affinities_author",
		"ALTER TABLE user_author_affinities ADD CONSTRAINT fk_user_author_affinities_author FOREIGN KEY (author_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS fk_user_reco_profile_dirty_user",
		"ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT fk_user_reco_profile_dirty_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE user_article_reco_states DROP CONSTRAINT IF EXISTS chk_user_article_reco_states_passive_signal",
		"ALTER TABLE user_article_reco_states ADD CONSTRAINT chk_user_article_reco_states_passive_signal CHECK (passive_signal IN ('', 'view', 'click', 'qualified_read', 'neutral_read', 'quick_bounce'))",
		"ALTER TABLE user_article_reco_states DROP CONSTRAINT IF EXISTS chk_user_article_reco_states_negative_signal",
		"ALTER TABLE user_article_reco_states ADD CONSTRAINT chk_user_article_reco_states_negative_signal CHECK (negative_signal IN ('', 'quick_bounce', 'not_interested'))",
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

func applyArticleEmbeddingConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE article_embeddings DROP CONSTRAINT IF EXISTS chk_article_embeddings_vector_dimensions",
		"ALTER TABLE article_embeddings ADD CONSTRAINT chk_article_embeddings_vector_dimensions CHECK (vector_dims(embedding) = dimensions)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply article embedding constraint: %w", err)
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
func applyArticleAuthorConstraints(tx *gorm.DB) error {
	if !tx.Migrator().HasConstraint(&models.Article{}, "Author") {
		if err := tx.Migrator().CreateConstraint(&models.Article{}, "Author"); err != nil {
			return fmt.Errorf("create article author foreign key: %w", err)
		}
	}
	if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_articles_author_published ON articles (author_id, published_at DESC, id DESC) WHERE deleted_at IS NULL AND publication_state = 'published' AND published_at IS NOT NULL").Error; err != nil {
		return fmt.Errorf("create article author index: %w", err)
	}
	return nil
}

func applyArticleEngagementConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE articles DROP CONSTRAINT IF EXISTS chk_articles_comment_count_nonnegative",
		"ALTER TABLE articles ADD CONSTRAINT chk_articles_comment_count_nonnegative CHECK (comment_count >= 0)",
		"UPDATE articles SET view_count = 0 WHERE view_count IS NULL",
		"ALTER TABLE articles ALTER COLUMN view_count SET DEFAULT 0",
		"ALTER TABLE articles ALTER COLUMN view_count SET NOT NULL",
		"ALTER TABLE articles DROP CONSTRAINT IF EXISTS chk_articles_view_count_nonnegative",
		"ALTER TABLE articles ADD CONSTRAINT chk_articles_view_count_nonnegative CHECK (view_count >= 0)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply article engagement constraint: %w", err)
		}
	}
	return nil
}

func applyCommentConstraints(tx *gorm.DB) error {
	if !tx.Migrator().HasConstraint(&models.Comment{}, "Article") {
		if err := tx.Migrator().CreateConstraint(&models.Comment{}, "Article"); err != nil {
			return fmt.Errorf("create comment article foreign key: %w", err)
		}
	}
	if !tx.Migrator().HasConstraint(&models.Comment{}, "Author") {
		if err := tx.Migrator().CreateConstraint(&models.Comment{}, "Author"); err != nil {
			return fmt.Errorf("create comment author foreign key: %w", err)
		}
	}
	if err := tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_comments_article_created
ON comments (article_id, created_at DESC, id DESC)
WHERE deleted_at IS NULL
`).Error; err != nil {
		return fmt.Errorf("create comment cursor index: %w", err)
	}
	return nil
}
func applyArticleReactionConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE article_reaction ALTER COLUMN liked DROP DEFAULT",
		"ALTER TABLE article_reaction ADD COLUMN IF NOT EXISTS state_changed_at TIMESTAMPTZ",
		"UPDATE article_reaction SET state_changed_at = COALESCE(state_changed_at, updated_at, CURRENT_TIMESTAMP)",
		"ALTER TABLE article_reaction ALTER COLUMN state_changed_at SET NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_article_reaction_user_liked_state_article ON article_reaction (user_id, liked, state_changed_at DESC, article_id)",
		"CREATE INDEX IF NOT EXISTS idx_article_behavior_user_view_seen ON article_behaviors (user_id, action, last_seen_at DESC, id DESC) WHERE action = 'view'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply article reaction constraint: %w", err)
		}
	}
	return nil
}

func applyLegacyAISchemaCleanup(tx *gorm.DB) error {
	statements := []string{
		"DROP TABLE IF EXISTS article_analysis_jobs",
		"ALTER TABLE articles DROP COLUMN IF EXISTS summary",
		"ALTER TABLE articles DROP COLUMN IF EXISTS tags",
		"ALTER TABLE articles DROP COLUMN IF EXISTS category",
		"ALTER TABLE articles DROP COLUMN IF EXISTS analysis_state",
		"ALTER TABLE articles DROP COLUMN IF EXISTS analysis_version",
		"DROP INDEX IF EXISTS idx_articles_recommendation",
		"DROP INDEX IF EXISTS idx_articles_recommendation_category_created",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("remove legacy AI schema: %w", err)
		}
	}
	return nil
}

func applyLegacyArticleEmbeddingJobCleanup(tx *gorm.DB) error {
	if err := tx.Exec("DROP TABLE IF EXISTS article_embedding_jobs").Error; err != nil {
		return fmt.Errorf("drop legacy article embedding jobs: %w", err)
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
		"DROP INDEX IF EXISTS idx_articles_recommendation_popular",
		"CREATE INDEX IF NOT EXISTS idx_articles_recommendation_recent ON articles (published_at DESC, id DESC) WHERE deleted_at IS NULL AND publication_state = 'published' AND published_at IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_articles_recommendation_trending ON articles (published_at DESC, id DESC) WHERE deleted_at IS NULL AND publication_state = 'published' AND published_at IS NOT NULL AND (like_count > 0 OR comment_count > 0)",
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
		"CREATE UNIQUE INDEX uidx_recommendation_result_trace_request_article ON recommendation_result_traces (request_id, article_id)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_article ON recommendation_result_traces (article_id)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_created ON recommendation_result_traces (created_at)",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_result_trace_expires ON recommendation_result_traces (expires_at)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS fk_recommendation_result_traces_request",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT fk_recommendation_result_traces_request FOREIGN KEY (request_id) REFERENCES recommendation_requests(request_id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS fk_recommendation_result_traces_article",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT fk_recommendation_result_traces_article FOREIGN KEY (article_id) REFERENCES articles(id) ON UPDATE CASCADE ON DELETE CASCADE",
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
		"ALTER TABLE recommendation_daily_metrics ADD CONSTRAINT recommendation_daily_metrics_pkey PRIMARY KEY (metric_date, scene, ranker_version, ranker_config_hash, strategy_id, exploration_opportunity, selection_mode, exploration_reason, position, article_id)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation exploration schema: %w", err)
		}
	}
	return nil
}
