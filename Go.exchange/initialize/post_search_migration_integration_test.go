package initialize

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostSearchSchema14MigrationIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	schema := fmt.Sprintf("post_search_migration_%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("run schema-14 migration: %v", err)
	}
	var extensions int64
	if err := tx.Raw("SELECT COUNT(*) FROM pg_extension WHERE extname = 'pg_trgm'").Scan(&extensions).Error; err != nil {
		t.Fatal(err)
	}
	if extensions != 1 {
		t.Fatalf("pg_trgm extension count=%d, want 1", extensions)
	}
	var indexes []struct {
		IndexName  string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	if err := tx.Raw(`SELECT indexname, indexdef FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname IN ('idx_posts_search_content_trgm', 'idx_posts_search_public_created')
ORDER BY indexname`).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Fatalf("search indexes=%+v, want both schema-14 indexes", indexes)
	}
	definitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		definitions[index.IndexName] = strings.ToLower(index.Definition)
	}
	if definition := definitions["idx_posts_search_content_trgm"]; !strings.Contains(definition, "using gin") || !strings.Contains(definition, "gin_trgm_ops") || !strings.Contains(definition, "deleted_at is null") || !strings.Contains(definition, "visibility = 'public'") {
		t.Fatalf("unexpected trigram index definition: %q", definition)
	}
	if definition := definitions["idx_posts_search_public_created"]; !strings.Contains(definition, "created_at desc") || !strings.Contains(definition, "id desc") || !strings.Contains(definition, "deleted_at is null") || !strings.Contains(definition, "visibility = 'public'") {
		t.Fatalf("unexpected latest-search index definition: %q", definition)
	}
	var state models.RuntimeSchemaState
	if err := tx.First(&state, runtimeSchemaStateID).Error; err != nil {
		t.Fatal(err)
	}
	if state.CurrentVersion != 15 || state.CompatibilityFloor != 14 {
		t.Fatalf("runtime schema=%d/%d, want 15/14", state.CurrentVersion, state.CompatibilityFloor)
	}
	if time.Since(state.AppliedAt) > time.Minute {
		t.Fatalf("schema migration did not publish a fresh applied_at: %s", state.AppliedAt)
	}
}
