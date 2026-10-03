package initialize

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
)

func TestPostSearchSchema14MigrationIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
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
		IndexName string
		Method    string
		Valid     bool
		Ready     bool
		Unique    bool
		Columns   string
		Opclasses string
		Options   string
		Predicate string
	}
	if err := tx.Raw(`SELECT idx.relname AS index_name, am.amname AS method,
       i.indisvalid AS valid, i.indisready AS ready, i.indisunique AS unique,
       string_agg(a.attname, ',' ORDER BY k.ordinality) AS columns,
       string_agg(op.opcname, ',' ORDER BY k.ordinality) AS opclasses,
       string_agg(k.options::text, ',' ORDER BY k.ordinality) AS options,
       pg_get_expr(i.indpred, i.indrelid) AS predicate
FROM pg_index i
JOIN pg_class idx ON idx.oid = i.indexrelid
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
JOIN pg_am am ON am.oid = idx.relam
CROSS JOIN LATERAL unnest(i.indkey::smallint[], i.indclass::oid[], i.indoption::smallint[])
  WITH ORDINALITY AS k(attnum, opclass_oid, options, ordinality)
JOIN pg_attribute a ON a.attrelid = tbl.oid AND a.attnum = k.attnum
JOIN pg_opclass op ON op.oid = k.opclass_oid
WHERE ns.nspname = current_schema() AND tbl.relname = 'posts'
  AND idx.relname IN ('idx_posts_search_content_trgm', 'idx_posts_search_public_created')
GROUP BY idx.relname, am.amname, i.indisvalid, i.indisready, i.indisunique, i.indpred, i.indrelid
ORDER BY idx.relname`).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Fatalf("search indexes=%+v, want both schema-14 indexes", indexes)
	}
	for _, index := range indexes {
		// pg_get_expr may add text casts and parentheses. Compare the complete
		// conjunction, so an OR, missing clause or different visibility fails.
		predicate := strings.NewReplacer("::text", "", "(", "", ")", "", `"`, "").Replace(strings.ToLower(index.Predicate))
		predicate = strings.Join(strings.Fields(predicate), "")
		if !index.Valid || !index.Ready || index.Unique || predicate != "deleted_atisnullandvisibility='public'" {
			t.Fatalf("invalid public-search index metadata: %+v", index)
		}
		switch index.IndexName {
		case "idx_posts_search_content_trgm":
			if index.Method != "gin" || index.Columns != "content" || index.Opclasses != "gin_trgm_ops" {
				t.Fatalf("unexpected trigram access method/column/opclass: %+v", index)
			}
		case "idx_posts_search_public_created":
			if index.Method != "btree" || index.Columns != "created_at,id" || index.Options != "3,3" {
				t.Fatalf("latest-search index must order created_at and id DESC NULLS FIRST: %+v", index)
			}
		}
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
