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
			&models.UserFollow{},
			&models.Article{},
			&models.Comment{},
			&models.ArticleAnalysisJob{},
			&models.OutboxEvent{},
			&models.ConsumerInbox{},
			&models.ArticleBehavior{},
			&models.ArticleReaction{},
			&models.RecommendationEvent{},
			&models.RecommendationDailyMetric{},
			&models.RecommendationRequest{},
			&models.ExchangeRate{},
		); err != nil {
			return fmt.Errorf("auto migrate database: %w", err)
		}

		if err := applyUserFollowConstraints(tx); err != nil {
			return err
		}

		if err := applyRecommendationTelemetryConstraints(tx); err != nil {
			return err
		}
		if err := applyArticleReactionConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationRankerV3Indexes(tx); err != nil {
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
func applyRecommendationTelemetryConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_type",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_foreground_time",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_scroll_depth",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_scroll_progress",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_estimated_read_time",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_read_policy",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_read_outcome",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_exit_type",
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_read_payload",
		"ALTER TABLE recommendation_events DROP COLUMN IF EXISTS max_scroll_depth",
		"ALTER TABLE recommendation_events DROP COLUMN IF EXISTS qualified_read",
		"ALTER TABLE recommendation_events DROP COLUMN IF EXISTS quick_bounce",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_type CHECK (event_type IN ('impression','click','read_end','not_interested'))",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_foreground_time CHECK (foreground_time_ms IS NULL OR foreground_time_ms BETWEEN 0 AND 21600000)",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_scroll_progress CHECK (scroll_progress_percent IS NULL OR scroll_progress_percent BETWEEN 0 AND 100)",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_estimated_read_time CHECK (estimated_read_time_ms IS NULL OR estimated_read_time_ms > 0)",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_read_policy CHECK (read_policy_version IS NULL OR btrim(read_policy_version) <> '')",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_read_outcome CHECK (read_outcome IS NULL OR read_outcome IN ('qualified','quick_bounce','neutral'))",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_exit_type CHECK (exit_type IS NULL OR exit_type IN ('back_to_recommendation','navigate_to_article','route_leave','page_hide','refresh','unknown'))",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_read_payload CHECK ((event_type = 'read_end' AND foreground_time_ms IS NOT NULL AND scroll_progress_percent IS NOT NULL AND exit_type IS NOT NULL AND estimated_read_time_ms IS NOT NULL AND read_policy_version IS NOT NULL AND read_outcome IS NOT NULL) OR (event_type <> 'read_end' AND foreground_time_ms IS NULL AND scroll_progress_percent IS NULL AND exit_type IS NULL AND estimated_read_time_ms IS NULL AND read_policy_version IS NULL AND read_outcome IS NULL))",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation telemetry constraint: %w", err)
		}
	}
	return nil
}

func applyArticleReactionConstraints(tx *gorm.DB) error {
	statements := []string{
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

func applyRecommendationRankerV3Indexes(tx *gorm.DB) error {
	statements := []string{
		"DROP INDEX IF EXISTS idx_recommendation_events_user_feedback_order",
		"DROP INDEX IF EXISTS idx_recommendation_events_user_article_negative",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_events_user_feedback_article_order ON recommendation_events (user_id, article_id, event_type, occurred_at DESC, received_at DESC, event_id DESC) WHERE event_type IN ('click', 'read_end', 'not_interested')",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_events_user_article_negative_order ON recommendation_events (user_id, article_id, occurred_at DESC, received_at DESC, event_id DESC) WHERE event_type = 'not_interested'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation ranker v3 index: %w", err)
		}
	}
	return nil
}
