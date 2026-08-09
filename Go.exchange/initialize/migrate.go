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

		if err := applyRecommendationTelemetryConstraints(tx); err != nil {
			return err
		}
		if err := applyRecommendationRankerV2Indexes(tx); err != nil {
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

func applyArticleAuthorConstraints(tx *gorm.DB) error {
	if !tx.Migrator().HasConstraint(&models.Article{}, "Author") {
		if err := tx.Migrator().CreateConstraint(&models.Article{}, "Author"); err != nil {
			return fmt.Errorf("create article author foreign key: %w", err)
		}
	}
	if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_articles_author_created ON articles (author_id, created_at DESC, id DESC)").Error; err != nil {
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
		"ALTER TABLE recommendation_events DROP CONSTRAINT IF EXISTS chk_recommendation_event_exit_type",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_type CHECK (event_type IN ('impression','click','read_end','not_interested'))",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_foreground_time CHECK (foreground_time_ms IS NULL OR foreground_time_ms BETWEEN 0 AND 21600000)",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_scroll_depth CHECK (max_scroll_depth IS NULL OR max_scroll_depth BETWEEN 0 AND 100)",
		"ALTER TABLE recommendation_events ADD CONSTRAINT chk_recommendation_event_exit_type CHECK (exit_type IS NULL OR exit_type IN ('back_to_recommendation','navigate_to_article','route_leave','page_hide','refresh','unknown'))",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation telemetry constraint: %w", err)
		}
	}
	return nil
}

func applyRecommendationRankerV2Indexes(tx *gorm.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_recommendation_events_user_feedback_order ON recommendation_events (user_id, occurred_at DESC, received_at DESC, event_id DESC) WHERE event_type IN ('click', 'read_end', 'not_interested')",
		"CREATE INDEX IF NOT EXISTS idx_recommendation_events_user_article_negative ON recommendation_events (user_id, article_id, occurred_at DESC) WHERE event_type = 'not_interested'",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation ranker v2 index: %w", err)
		}
	}
	return nil
}
