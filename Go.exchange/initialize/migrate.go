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

		if err := tx.AutoMigrate(
			&models.User{},
			&models.UserFollow{},
			&models.Article{},
			&models.Comment{},
			&models.ArticleEmbedding{},
			&models.ArticleEmbeddingJob{},
			&models.OutboxEvent{},
			&models.ConsumerInbox{},
			&models.ArticleBehavior{},
			&models.ArticleReaction{},
			&models.RecommendationDailyMetric{},
			&models.RecommendationRequest{},
			&models.ExchangeRate{},
		); err != nil {
			return fmt.Errorf("auto migrate database: %w", err)
		}

		if err := applyLegacyAISchemaCleanup(tx); err != nil {
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
		if err := applyRecommendationRetrievalV1Indexes(tx); err != nil {
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
		"DROP INDEX IF EXISTS idx_articles_recommendation_popular",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("remove legacy AI schema: %w", err)
		}
	}
	return nil
}

func applyRecommendationRetrievalV1Indexes(tx *gorm.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_articles_recommendation_recent ON articles (created_at DESC, id DESC) WHERE deleted_at IS NULL AND publication_state = 'published' AND published_at IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS idx_articles_recommendation_popular ON articles (like_count DESC, created_at DESC, id DESC) WHERE deleted_at IS NULL AND publication_state = 'published' AND published_at IS NOT NULL",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation retrieval v1 index: %w", err)
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
