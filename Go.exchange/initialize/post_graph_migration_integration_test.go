package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostGraphSchemaIntegration(t *testing.T) {
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
		Name string `gorm:"column:column_name"`
	}
	if err := db.Raw(`
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'posts'
`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	columnSet := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		columnSet[column.Name] = struct{}{}
	}
	for _, required := range []string{
		"author_id", "content", "reply_to_post_id", "quote_post_id", "conversation_id",
		"visibility", "like_count", "reply_count", "view_count", "like_sync_version",
	} {
		if _, ok := columnSet[required]; !ok {
			t.Fatalf("posts is missing canonical column %q; columns=%v", required, columnSet)
		}
	}
	for _, forbidden := range []string{"title", "preview", "cover_image_url", "publication_state", "published_at", "expired_at", "comment_count"} {
		if _, ok := columnSet[forbidden]; ok {
			t.Fatalf("posts contains forbidden Article-era column %q", forbidden)
		}
	}
	var postArticleTables int64
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_name = 'post_articles'
`).Scan(&postArticleTables).Error; err != nil {
		t.Fatal(err)
	}
	if postArticleTables != 0 {
		t.Fatalf("post_articles table still exists")
	}
	var postMediaTables int64
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_name = 'post_media'
`).Scan(&postMediaTables).Error; err != nil {
		t.Fatal(err)
	}
	if postMediaTables != 1 {
		t.Fatalf("post_media table is missing")
	}

	var mediaConstraints []struct {
		Name       string `gorm:"column:conname"`
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT conname, pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = 'post_media'::regclass
`).Scan(&mediaConstraints).Error; err != nil {
		t.Fatal(err)
	}
	mediaDefinitions := make(map[string]string, len(mediaConstraints))
	for _, constraint := range mediaConstraints {
		mediaDefinitions[constraint.Name] = strings.ToLower(strings.Join(strings.Fields(constraint.Definition), ""))
	}
	for name, requiredParts := range map[string][]string{
		"fk_post_media_post":          {"foreignkey(post_id)", "referencesposts(id)", "ondeletecascade"},
		"chk_post_media_type":         {"media_type", "image"},
		"chk_post_media_position":     {"position>=0", "position<=3"},
		"chk_post_media_url_nonblank": {"char_length", "trim", "url", ">0"},
	} {
		definition, ok := mediaDefinitions[name]
		if !ok {
			t.Fatalf("missing PostMedia constraint %q", name)
		}
		for _, part := range requiredParts {
			if !strings.Contains(definition, part) {
				t.Fatalf("PostMedia constraint %q definition=%q missing %q", name, definition, part)
			}
		}
	}

	var mediaIndexes []struct {
		Name       string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'post_media'
`).Scan(&mediaIndexes).Error; err != nil {
		t.Fatal(err)
	}
	mediaIndexDefinitions := make(map[string]string, len(mediaIndexes))
	for _, index := range mediaIndexes {
		definition := strings.ToLower(strings.Join(strings.Fields(index.Definition), ""))
		mediaIndexDefinitions[index.Name] = strings.ReplaceAll(definition, string(rune(34)), "")
	}
	if definition, ok := mediaIndexDefinitions["uidx_post_media_post_position"]; !ok || !strings.Contains(definition, "uniqueindex") || !strings.Contains(definition, "(post_id,position)") {
		t.Fatalf("missing or invalid PostMedia unique index: %#v", mediaIndexDefinitions)
	}

	var constraints []struct {
		Name       string `gorm:"column:conname"`
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT conname, pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = 'posts'::regclass
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	constraintDefinitions := make(map[string]string, len(constraints))
	for _, constraint := range constraints {
		constraintDefinitions[constraint.Name] = strings.ToLower(strings.Join(strings.Fields(constraint.Definition), ""))
	}
	foreignKeys := map[string][]string{
		"fk_posts_author":        {"foreignkey(author_id)", "referencesusers(id)"},
		"fk_posts_reply_to_post": {"foreignkey(reply_to_post_id)", "referencesposts(id)"},
		"fk_posts_quote_post":    {"foreignkey(quote_post_id)", "referencesposts(id)"},
		"fk_posts_conversation":  {"foreignkey(conversation_id)", "referencesposts(id)"},
	}
	for name, requiredParts := range foreignKeys {
		definition, ok := constraintDefinitions[name]
		if !ok {
			t.Fatalf("missing Post graph foreign key %q", name)
		}
		for _, part := range requiredParts {
			if !strings.Contains(definition, part) {
				t.Fatalf("foreign key %q definition=%q missing %q", name, definition, part)
			}
		}
	}
	for _, name := range []string{"chk_posts_reply_quote_exclusive", "chk_posts_conversation_shape"} {
		if _, ok := constraintDefinitions[name]; !ok {
			t.Fatalf("missing Post graph constraint %q", name)
		}
	}
	for name, column := range map[string]string{
		"chk_posts_like_count_nonnegative":        "like_count>=0",
		"chk_posts_reply_count_nonnegative":       "reply_count>=0",
		"chk_posts_view_count_nonnegative":        "view_count>=0",
		"chk_posts_like_sync_version_nonnegative": "like_sync_version>=0",
	} {
		definition, ok := constraintDefinitions[name]
		if !ok || !strings.Contains(definition, column) {
			t.Fatalf("counter constraint %q definition=%q", name, definition)
		}
	}

	var indexes []struct {
		Name       string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'posts'
`).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	indexDefinitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		indexDefinitions[index.Name] = strings.ToLower(strings.Join(strings.Fields(index.Definition), ""))
	}
	indexRequirements := map[string][]string{
		"idx_posts_author_created":       {"author_id", "created_atdesc", "iddesc"},
		"idx_posts_reply_to_created":     {"reply_to_post_id", "created_atdesc", "iddesc"},
		"idx_posts_conversation_created": {"conversation_id", "created_atdesc", "iddesc"},
		"idx_posts_quote":                {"quote_post_id"},
	}
	for name, requiredParts := range indexRequirements {
		definition, ok := indexDefinitions[name]
		if !ok {
			t.Fatalf("missing Post graph index %q", name)
		}
		for _, part := range requiredParts {
			if !strings.Contains(definition, part) {
				t.Fatalf("index %q definition=%q missing %q", name, definition, part)
			}
		}
	}
}
