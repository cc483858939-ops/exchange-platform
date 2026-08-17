package initialize

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationBehaviorProjectionSchemaIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ArticleBehavior{}); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&models.ArticleBehavior{}) {
		t.Fatal("article_behaviors projection table is missing")
	}

	var uniqueIndexCount int
	if err := db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'article_behaviors' AND indexdef ILIKE 'CREATE UNIQUE INDEX%' AND indexdef ILIKE '%user_id%' AND indexdef ILIKE '%article_id%' AND indexdef ILIKE '%action%'").Scan(&uniqueIndexCount).Error; err != nil {
		t.Fatal(err)
	}
	if uniqueIndexCount == 0 {
		t.Fatal("article_behaviors unique user/article/action index is missing")
	}
}

func TestRunMigrationsIsIdempotentIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	if err := RunMigrations(); err != nil {
		t.Fatalf("first migration run: %v", err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	if !db.Migrator().HasTable(&models.ArticleBehavior{}) {
		t.Fatal("article_behaviors projection table is missing after migrations")
	}

	var metricColumns []struct {
		Name     string `gorm:"column:name"`
		Nullable string `gorm:"column:nullable"`
	}
	metricQuery := "SELECT column_name AS name, is_nullable AS nullable " +
		"FROM information_schema.columns " +
		"WHERE table_schema = current_schema() AND table_name = 'recommendation_daily_metrics' " +
		"AND column_name IN ('feed_dwell_count', 'feed_visible_time_ms') ORDER BY column_name"
	if err := db.Raw(metricQuery).Scan(&metricColumns).Error; err != nil {
		t.Fatal(err)
	}
	if len(metricColumns) != 2 {
		t.Fatalf("recommendation metric columns=%d want=2: %#v", len(metricColumns), metricColumns)
	}
	for _, column := range metricColumns {
		if column.Nullable != "NO" {
			t.Fatalf("%s nullable=%q want NO", column.Name, column.Nullable)
		}
	}
	var reactionColumn struct {
		Nullable string         `gorm:"column:nullable"`
		Default  sql.NullString `gorm:"column:column_default"`
	}
	reactionQuery := "SELECT is_nullable AS nullable, column_default AS column_default " +
		"FROM information_schema.columns " +
		"WHERE table_schema = current_schema() AND table_name = 'article_reaction' AND column_name = 'liked'"
	if err := db.Raw(reactionQuery).Scan(&reactionColumn).Error; err != nil {
		t.Fatal(err)
	}
	if reactionColumn.Nullable != "NO" {
		t.Fatalf("article_reaction.liked nullable=%q want NO", reactionColumn.Nullable)
	}
	if reactionColumn.Default.Valid {
		t.Fatalf("article_reaction.liked default=%q want NULL", reactionColumn.Default.String)
	}

	assertRecommendationRetrievalV1Indexes(t, db)
}

func TestArticleAuthorConstraintsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}); err != nil {
		t.Fatal(err)
	}
	if err := applyArticleAuthorConstraints(db); err != nil {
		t.Fatal(err)
	}

	var nullable string
	nullableQuery := "SELECT is_nullable FROM information_schema.columns " +
		"WHERE table_schema = current_schema() AND table_name = 'articles' AND column_name = 'author_id'"
	if err := db.Raw(nullableQuery).Scan(&nullable).Error; err != nil {
		t.Fatal(err)
	}
	if nullable != "NO" {
		t.Fatalf("articles.author_id is_nullable=%q want NO", nullable)
	}

	var constraints []struct {
		Definition string
	}
	constraintQuery := "SELECT pg_get_constraintdef(c.oid) AS definition FROM pg_constraint c " +
		"JOIN pg_class t ON t.oid = c.conrelid WHERE t.relname = 'articles' AND c.contype = 'f'"
	if err := db.Raw(constraintQuery).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	if len(constraints) == 0 {
		t.Fatal("article author foreign key is missing")
	}
	definition := strings.ToLower(constraints[0].Definition)
	assertIndexDefinitionContains(t, definition, "author_id", "references", "users", "on update cascade", "on delete restrict")

	var indexDefinition string
	indexQuery := "SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() " +
		"AND tablename = 'articles' AND indexname = 'idx_articles_author_published'"
	if err := db.Raw(indexQuery).Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	assertIndexDefinitionContains(t, strings.ToLower(indexDefinition), "author_id", "published_at desc", "id desc", "deleted_at is null", "publication_state", "published_at is not null")

	var oldIndexCount int
	if err := db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'articles' AND indexname = 'idx_articles_author_created'").Scan(&oldIndexCount).Error; err != nil {
		t.Fatal(err)
	}
	if oldIndexCount != 0 {
		t.Fatalf("legacy article author index still exists: %d", oldIndexCount)
	}
}

func assertRecommendationRetrievalV1Indexes(t *testing.T, db *gorm.DB) {
	t.Helper()
	var indexes []struct {
		Name       string
		Definition string
	}
	query := "SELECT indexname AS name, indexdef AS definition FROM pg_indexes " +
		"WHERE schemaname = current_schema() AND tablename = 'articles' " +
		"AND indexname IN ('idx_articles_recommendation_category_created', 'idx_articles_recommendation_popular') " +
		"ORDER BY indexname"
	if err := db.Raw(query).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Fatalf("recommendation retrieval indexes=%d want=2", len(indexes))
	}
	definitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		definitions[index.Name] = strings.ToLower(index.Definition)
	}
	assertIndexDefinitionContains(t, definitions["idx_articles_recommendation_category_created"],
		"lower", "btrim", "created_at desc", "id desc", "deleted_at is null", "publication_state", "analysis_state", "published_at is not null")
	assertIndexDefinitionContains(t, definitions["idx_articles_recommendation_popular"],
		"like_count desc", "created_at desc", "id desc", "deleted_at is null", "publication_state", "analysis_state", "published_at is not null")
}

func assertIndexDefinitionContains(t *testing.T, definition string, fragments ...string) {
	t.Helper()
	if definition == "" {
		t.Fatal("index definition is missing")
	}
	for _, fragment := range fragments {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("index definition %q does not contain %q", definition, fragment)
		}
	}
}
