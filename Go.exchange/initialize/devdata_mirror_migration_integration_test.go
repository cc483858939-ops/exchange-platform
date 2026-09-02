package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDevDataMirrorSchemaIntegrationIsIdempotent(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	previous := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previous })
	if err := RunMigrations(); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	for table, columns := range map[string][]string{
		"devdata_mirror_accounts": {"id", "registry_key", "platform", "source_user_id", "source_handle", "local_user_id", "category", "enabled", "last_fetched_at", "created_at", "updated_at"},
		"devdata_mirror_posts":    {"id", "platform", "source_post_id", "source_url", "mirror_account_id", "local_post_id", "source_created_at", "source_like_count", "source_reply_count", "source_repost_count", "source_quote_count", "content_hash", "state", "imported_at", "created_at", "updated_at"},
	} {
		var rows []struct {
			Name string `gorm:"column:column_name"`
		}
		if err := db.Raw(`
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = ?
`, table).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Name] = struct{}{}
		}
		for _, column := range columns {
			if _, ok := seen[column]; !ok {
				t.Fatalf("table %s missing column %s", table, column)
			}
		}
	}

	var constraints []struct {
		Table      string `gorm:"column:table_name"`
		Name       string `gorm:"column:conname"`
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT class.relname AS table_name, constraints_catalog.conname,
       pg_get_constraintdef(constraints_catalog.oid) AS definition
FROM pg_constraint AS constraints_catalog
JOIN pg_class AS class ON class.oid = constraints_catalog.conrelid
JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = current_schema()
  AND class.relname IN ('devdata_mirror_accounts', 'devdata_mirror_posts')
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	definitions := make(map[string]string, len(constraints))
	for _, constraint := range constraints {
		definitions[constraint.Name] = strings.ToLower(strings.Join(strings.Fields(constraint.Definition), ""))
	}
	for _, name := range []string{
		"ucon_devdata_mirror_accounts_registry_key",
		"ucon_devdata_mirror_accounts_platform_source_user",
		"ucon_devdata_mirror_accounts_local_user",
		"fk_devdata_mirror_accounts_local_user",
		"ucon_devdata_mirror_posts_platform_source_post",
		"ucon_devdata_mirror_posts_local_post",
		"fk_devdata_mirror_posts_account",
		"fk_devdata_mirror_posts_post",
		"chk_devdata_mirror_posts_state",
		"chk_devdata_mirror_posts_source_metrics",
	} {
		if _, ok := definitions[name]; !ok {
			t.Fatalf("missing DevData constraint %q", name)
		}
	}
	if !strings.Contains(definitions["fk_devdata_mirror_accounts_local_user"], "ondeleterestrict") {
		t.Fatalf("local user FK=%q", definitions["fk_devdata_mirror_accounts_local_user"])
	}
	if !strings.Contains(definitions["fk_devdata_mirror_posts_account"], "ondeletecascade") || !strings.Contains(definitions["fk_devdata_mirror_posts_post"], "ondeletecascade") {
		t.Fatalf("mapping FKs account=%q post=%q", definitions["fk_devdata_mirror_posts_account"], definitions["fk_devdata_mirror_posts_post"])
	}

	var indexCount int64
	if err := db.Raw(`
SELECT COUNT(*)
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = 'idx_devdata_mirror_accounts_enabled'
`).Scan(&indexCount).Error; err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("enabled account index count=%d", indexCount)
	}
	if err := db.Raw(`
SELECT COUNT(*)
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = 'idx_devdata_mirror_posts_account_state'
`).Scan(&indexCount).Error; err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("account/state index count=%d", indexCount)
	}

	for table, forbidden := range map[string]string{"users": "devdata_registry_key", "posts": "devdata_source_post_id"} {
		var count int64
		if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, forbidden).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("table %s unexpectedly contains %s", table, forbidden)
		}
	}
}
