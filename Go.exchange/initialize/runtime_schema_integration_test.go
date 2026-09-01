package initialize

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestRuntimeSchemaIntegrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL test database handle: %v", err)
	}
	defer sqlDB.Close()

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin isolated schema transaction: %v", tx.Error)
	}
	defer tx.Rollback()

	suffix := time.Now().UnixNano()
	primarySchema := fmt.Sprintf("runtime_readiness_primary_%d", suffix)
	fallbackSchema := fmt.Sprintf("runtime_readiness_fallback_%d", suffix)
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(primarySchema)).Error; err != nil {
		t.Fatalf("create primary isolated schema: %v", err)
	}
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(fallbackSchema)).Error; err != nil {
		t.Fatalf("create fallback isolated schema: %v", err)
	}
	if err := setIntegrationSearchPath(tx, primarySchema); err != nil {
		t.Fatalf("set primary schema migration search path: %v", err)
	}
	if err := tx.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatalf("enable pgvector for isolated schema: %v", err)
	}

	modelsToMigrate := append([]interface{}(nil), apiSchemaModels...)
	modelsToMigrate = append(modelsToMigrate, workerSchemaModels...)
	if err := tx.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("migrate primary isolated schema: %v", err)
	}
	if err := setIntegrationSearchPath(tx, fallbackSchema); err != nil {
		t.Fatalf("set fallback schema migration search path: %v", err)
	}
	if err := tx.AutoMigrate(&models.ExchangeRate{}); err != nil {
		t.Fatalf("migrate fallback exchange rate table: %v", err)
	}
	if err := setIntegrationSearchPath(tx, primarySchema); err != nil {
		t.Fatalf("restore primary schema migration search path: %v", err)
	}
	var currentSchema string
	if err := tx.Raw("SELECT current_schema()").Scan(&currentSchema).Error; err != nil {
		t.Fatalf("read current migration schema: %v", err)
	}
	if currentSchema != primarySchema {
		t.Fatalf("unexpected migration schema: got %q want %q", currentSchema, primarySchema)
	}
	if err := insertIntegrationState(tx, PublishedSchemaCurrentVersion, PublishedSchemaCompatibilityFloor); err != nil {
		t.Fatalf("insert runtime schema state: %v", err)
	}
	if err := applyPostSchemaConstraints(tx); err != nil {
		t.Fatalf("apply Post schema constraints: %v", err)
	}
	if err := applyPostArticleConstraints(tx); err != nil {
		t.Fatalf("apply PostArticle constraints: %v", err)
	}
	if err := applyPostEmbeddingConstraints(tx); err != nil {
		t.Fatalf("apply PostEmbedding constraints: %v", err)
	}
	if err := applyPostRepostConstraints(tx); err != nil {
		t.Fatalf("apply PostRepost constraints: %v", err)
	}
	if err := applyPostReactionConstraints(tx); err != nil {
		t.Fatalf("apply PostReaction constraints: %v", err)
	}
	if err := applyPostBehaviorConstraints(tx); err != nil {
		t.Fatalf("apply PostBehavior constraints: %v", err)
	}
	if err := applyRecommendationProfileMaterializationSchema(tx); err != nil {
		t.Fatalf("apply UserPostRecoState constraints: %v", err)
	}
	if err := setIntegrationValidationSearchPath(tx, primarySchema, fallbackSchema); err != nil {
		t.Fatalf("restore isolated schema validation search path: %v", err)
	}

	apiOptions := SchemaValidationOptions{RequiredVersion: RequiredSchemaVersion, EmbeddingEnabled: false}
	workerOptions := SchemaValidationOptions{RequiredVersion: RequiredSchemaVersion, IncludeWorkerTables: true}
	expectIntegrationSchemaCode(t, tx, apiOptions, "")
	expectIntegrationSchemaCode(t, tx, workerOptions, "")

	withIntegrationSavepoint(t, tx, "missing_post_constraint", func() {
		if err := tx.Exec("ALTER TABLE " + qualifiedIntegrationTable(primarySchema, "posts") + " DROP CONSTRAINT chk_posts_visibility_public").Error; err != nil {
			t.Fatalf("drop Post visibility constraint: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_constraint_missing")
	})

	withIntegrationSavepoint(t, tx, "missing_post_index", func() {
		if err := tx.Exec("DROP INDEX " + qualifiedIntegrationTable(primarySchema, "idx_posts_author_created")).Error; err != nil {
			t.Fatalf("drop Post author index: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_index_missing")
	})

	withIntegrationSavepoint(t, tx, "legacy_content_table", func() {
		if err := tx.Exec("CREATE TABLE " + qualifiedIntegrationTable(primarySchema, "articles") + " (id BIGINT)").Error; err != nil {
			t.Fatalf("create legacy articles table: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_legacy_content_present")
	})

	withIntegrationSavepoint(t, tx, "legacy_runtime_column", func() {
		if err := tx.Exec("ALTER TABLE " + qualifiedIntegrationTable(primarySchema, "notifications") + " ADD COLUMN article_id BIGINT").Error; err != nil {
			t.Fatalf("add legacy notification column: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_legacy_content_present")
	})

	withIntegrationSavepoint(t, tx, "missing_state", func() {
		if err := tx.Exec("DROP TABLE " + qualifiedIntegrationTable(primarySchema, "runtime_schema_state")).Error; err != nil {
			t.Fatalf("drop runtime schema state table: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_state_missing")
	})

	withIntegrationSavepoint(t, tx, "current_zero", func() {
		if err := updateIntegrationState(tx, primarySchema, 0, PublishedSchemaCompatibilityFloor); err != nil {
			t.Fatalf("set zero current schema version: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_incompatible")
	})

	withIntegrationSavepoint(t, tx, "floor_too_high", func() {
		if err := updateIntegrationState(tx, primarySchema, 2, 2); err != nil {
			t.Fatalf("set incompatible schema floor: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_incompatible")
	})

	withIntegrationSavepoint(t, tx, "compatible_newer", func() {
		if err := updateIntegrationState(tx, primarySchema, 2, 1); err != nil {
			t.Fatalf("set compatible newer schema version: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "")
	})

	withIntegrationSavepoint(t, tx, "missing_profile_table", func() {
		if err := tx.Exec("DROP TABLE " + qualifiedIntegrationTable(primarySchema, "user_reco_profiles")).Error; err != nil {
			t.Fatalf("drop user recommendation profile table: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_table_missing")
	})

	withIntegrationSavepoint(t, tx, "missing_embedding_column", func() {
		if err := tx.Exec("ALTER TABLE " + qualifiedIntegrationTable(primarySchema, "post_embeddings") + " DROP COLUMN " + quoteIntegrationIdentifier("embedding")).Error; err != nil {
			t.Fatalf("drop article embedding column: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_column_missing")
	})

	withIntegrationSavepoint(t, tx, "missing_worker_table", func() {
		if err := tx.Exec("DROP TABLE " + qualifiedIntegrationTable(primarySchema, "consumer_inboxes")).Error; err != nil {
			t.Fatalf("drop Worker consumer inbox table: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "")
		expectIntegrationSchemaCode(t, tx, workerOptions, "schema_table_missing")
	})

	withIntegrationSavepoint(t, tx, "primary_missing_exchange_column", func() {
		if err := tx.Exec("ALTER TABLE " + qualifiedIntegrationTable(primarySchema, "exchange_rates") + " DROP COLUMN " + quoteIntegrationIdentifier("to_currency")).Error; err != nil {
			t.Fatalf("drop primary exchange rate column: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_column_missing")
	})

	withIntegrationSavepoint(t, tx, "primary_exchange_view", func() {
		primaryExchange := qualifiedIntegrationTable(primarySchema, "exchange_rates")
		sourceTable := qualifiedIntegrationTable(primarySchema, "exchange_rates_view_source")
		if err := tx.Exec("CREATE TABLE " + sourceTable + " AS SELECT * FROM " + primaryExchange + " WITH NO DATA").Error; err != nil {
			t.Fatalf("create exchange rate view source: %v", err)
		}
		if err := tx.Exec("DROP TABLE " + primaryExchange).Error; err != nil {
			t.Fatalf("drop primary exchange rate table for view case: %v", err)
		}
		viewColumns := []string{
			quoteIntegrationIdentifier("id"),
			quoteIntegrationIdentifier("from_currency"),
			quoteIntegrationIdentifier("to_currency"),
			quoteIntegrationIdentifier("rate"),
			quoteIntegrationIdentifier("date"),
		}
		viewSQL := "CREATE VIEW " + primaryExchange + " AS SELECT " + strings.Join(viewColumns, ", ") + " FROM " + sourceTable
		if err := tx.Exec(viewSQL).Error; err != nil {
			t.Fatalf("create primary exchange rate view: %v", err)
		}
		expectIntegrationSchemaCode(t, tx, apiOptions, "schema_relation_invalid")
	})

	expectIntegrationSchemaCode(t, tx, apiOptions, "")
	expectIntegrationSchemaCode(t, tx, workerOptions, "")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expectIntegrationSchemaCodeWithContext(t, canceled, tx, apiOptions, "schema_check_timeout")
	if got := SchemaReasonCode(CheckRuntimeSchema(context.Background(), nil, apiOptions)); got != "schema_database_unavailable" {
		t.Fatalf("unexpected nil database reason: %q", got)
	}

	closedDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open closed-database probe: %v", err)
	}
	closedSQLDB, err := closedDB.DB()
	if err != nil {
		t.Fatalf("get closed-database probe handle: %v", err)
	}
	closedSQLDB.Close()
	expectIntegrationSchemaCode(t, closedDB, apiOptions, "schema_state_unavailable")
}

func insertIntegrationState(tx *gorm.DB, current, floor int64) error {
	return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&models.RuntimeSchemaState{
		ID:                 runtimeSchemaStateID,
		CurrentVersion:     current,
		CompatibilityFloor: floor,
		AppliedAt:          time.Now().UTC(),
		ReleaseRevision:    "integration-test",
	}).Error
}

func expectIntegrationSchemaCode(t *testing.T, db *gorm.DB, options SchemaValidationOptions, expected string) {
	t.Helper()
	expectIntegrationSchemaCodeWithContext(t, context.Background(), db, options, expected)
}

func expectIntegrationSchemaCodeWithContext(t *testing.T, ctx context.Context, db *gorm.DB, options SchemaValidationOptions, expected string) {
	t.Helper()
	err := CheckRuntimeSchema(ctx, db, options)
	if expected == "" {
		if err != nil {
			t.Fatalf("expected runtime schema check success, got %v", err)
		}
		return
	}
	if got := SchemaReasonCode(err); got != expected {
		t.Fatalf("expected schema reason %q, got %q (err=%v)", expected, got, err)
	}
}

func quoteIntegrationIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func qualifiedIntegrationTable(schema, table string) string {
	return quoteIntegrationIdentifier(schema) + "." + quoteIntegrationIdentifier(table)
}

func setIntegrationSearchPath(tx *gorm.DB, schemas ...string) error {
	parts := make([]string, 0, len(schemas)+1)
	for _, schema := range schemas {
		parts = append(parts, quoteIntegrationIdentifier(schema))
	}
	parts = append(parts, "public")
	return tx.Exec("SET LOCAL search_path TO " + strings.Join(parts, ", ")).Error
}

func setIntegrationValidationSearchPath(tx *gorm.DB, schemas ...string) error {
	parts := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		parts = append(parts, quoteIntegrationIdentifier(schema))
	}
	return tx.Exec("SET LOCAL search_path TO " + strings.Join(parts, ", ")).Error
}

func updateIntegrationState(tx *gorm.DB, schema string, current, floor int64) error {
	return tx.Exec(
		"UPDATE "+qualifiedIntegrationTable(schema, "runtime_schema_state")+" SET current_version = ?, compatibility_floor = ? WHERE id = ?",
		current, floor, runtimeSchemaStateID,
	).Error
}

func withIntegrationSavepoint(t *testing.T, tx *gorm.DB, name string, fn func()) {
	t.Helper()
	if err := tx.SavePoint(name).Error; err != nil {
		t.Fatalf("create savepoint %q: %v", name, err)
	}
	defer func() {
		if err := tx.RollbackTo(name).Error; err != nil {
			t.Errorf("rollback savepoint %q: %v", name, err)
		}
	}()
	fn()
}
