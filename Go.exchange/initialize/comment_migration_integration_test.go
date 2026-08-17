package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCommentMigrationIntegration(t *testing.T) {
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
		t.Fatal(err)
	}

	var columns []struct {
		Name     string `gorm:"column:column_name"`
		Nullable string `gorm:"column:is_nullable"`
	}
	if err := db.Raw(`
SELECT column_name, is_nullable
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'comments'
  AND column_name IN ('article_id', 'user_id', 'content')
`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if len(columns) != 3 {
		t.Fatalf("comment columns=%#v", columns)
	}
	for _, column := range columns {
		if column.Nullable != "NO" {
			t.Fatalf("comments.%s is_nullable=%q want NO", column.Name, column.Nullable)
		}
	}

	var constraints []struct {
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT pg_get_constraintdef(c.oid) AS definition
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
WHERE t.relname = 'comments'
  AND c.contype = 'f'
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	var articleFK, userFK bool
	for _, constraint := range constraints {
		definition := strings.ToLower(constraint.Definition)
		if strings.Contains(definition, "article_id") && strings.Contains(definition, "references articles") {
			articleFK = strings.Contains(definition, "on update cascade") && strings.Contains(definition, "on delete cascade")
		}
		if strings.Contains(definition, "user_id") && strings.Contains(definition, "references users") {
			userFK = strings.Contains(definition, "on update cascade") && strings.Contains(definition, "on delete restrict")
		}
	}
	if !articleFK || !userFK {
		t.Fatalf("comment foreign keys article=%t user=%t constraints=%#v", articleFK, userFK, constraints)
	}

	var indexDefinition string
	if err := db.Raw(`
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'comments'
  AND indexname = 'idx_comments_article_created'
`).Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"article_id", "created_at desc", "id desc", "deleted_at is null"} {
		if !strings.Contains(strings.ToLower(indexDefinition), expected) {
			t.Fatalf("index definition=%q missing %q", indexDefinition, expected)
		}
	}
}
