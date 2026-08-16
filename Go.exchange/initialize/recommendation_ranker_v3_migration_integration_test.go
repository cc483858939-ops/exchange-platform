package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationRankerV3IndexesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.RecommendationEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationRankerV3Indexes(db); err != nil {
		t.Fatal(err)
	}

	var indexes []struct {
		Name       string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'recommendation_events'
  AND indexname IN ('idx_recommendation_events_user_feedback_article_order', 'idx_recommendation_events_user_article_negative_order')
ORDER BY indexname
`).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Fatalf("rules_v3 indexes=%d want=2", len(indexes))
	}

	definitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		definitions[index.Name] = strings.ToLower(index.Definition)
	}
	assertIndexDefinitionContains(t, definitions["idx_recommendation_events_user_feedback_article_order"],
		"user_id", "article_id", "event_type", "occurred_at desc", "received_at desc", "event_id desc", "where")
	assertIndexDefinitionContains(t, definitions["idx_recommendation_events_user_article_negative_order"],
		"user_id", "article_id", "occurred_at desc", "received_at desc", "event_id desc", "not_interested", "where")
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

	var indexDefinition string
	if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'articles' AND indexname = 'idx_articles_author_published'").Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	assertIndexDefinitionContains(t, strings.ToLower(indexDefinition), "author_id", "published_at desc", "id desc", "deleted_at is null", "publication_state", "published_at is not null")
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
	if err := db.Raw(`
SELECT is_nullable
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'articles'
  AND column_name = 'author_id'
`).Scan(&nullable).Error; err != nil {
		t.Fatal(err)
	}
	if nullable != "NO" {
		t.Fatalf("articles.author_id is_nullable=%q want NO", nullable)
	}

	var constraints []struct {
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT pg_get_constraintdef(c.oid) AS definition
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
WHERE t.relname = 'articles'
  AND c.contype = 'f'
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	if len(constraints) == 0 {
		t.Fatal("article author foreign key is missing")
	}
	definition := strings.ToLower(constraints[0].Definition)
	assertIndexDefinitionContains(t, definition, "author_id", "references", "users", "on update cascade", "on delete restrict")

	var indexDefinition string
	if err := db.Raw(`
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'articles'
  AND indexname = 'idx_articles_author_published'
`).Scan(&indexDefinition).Error; err != nil {
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
