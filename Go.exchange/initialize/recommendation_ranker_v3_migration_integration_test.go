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

	var columns []struct {
		Name   string `gorm:"column:column_name"`
		Length *int   `gorm:"column:character_maximum_length"`
	}
	if err := db.Raw(`
SELECT column_name, character_maximum_length
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'recommendation_events'
  AND column_name IN ('read_policy_version', 'read_outcome')
ORDER BY column_name
`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 {
		t.Fatalf("recommendation telemetry columns=%d want=2: %#v", len(columns), columns)
	}
	wantLengths := map[string]int{
		"read_policy_version": 32,
		"read_outcome":        16,
	}
	for _, column := range columns {
		wantLength, ok := wantLengths[column.Name]
		if !ok {
			t.Fatalf("unexpected recommendation telemetry column %q", column.Name)
		}
		if column.Length == nil || *column.Length != wantLength {
			t.Fatalf("%s length=%v want=%d", column.Name, column.Length, wantLength)
		}
	}

	var constraints []string
	if err := db.Raw(`
SELECT c.conname
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
WHERE t.relname = 'recommendation_events'
  AND c.conname IN (
      'chk_recommendation_event_read_policy',
      'chk_recommendation_event_read_outcome',
      'chk_recommendation_event_read_payload'
  )
ORDER BY c.conname
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	foundConstraints := make(map[string]struct{}, len(constraints))
	for _, constraint := range constraints {
		foundConstraints[constraint] = struct{}{}
	}
	for _, want := range []string{
		"chk_recommendation_event_read_policy",
		"chk_recommendation_event_read_outcome",
		"chk_recommendation_event_read_payload",
	} {
		if _, ok := foundConstraints[want]; !ok {
			t.Fatalf("missing recommendation telemetry constraint %q; found=%v", want, constraints)
		}
	}
	if len(foundConstraints) != 3 {
		t.Fatalf("recommendation telemetry constraints=%v want exactly 3", constraints)
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
