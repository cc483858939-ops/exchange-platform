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

func TestPostQuoteCountBackfillFromSchema12Integration(t *testing.T) {
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
		t.Fatalf("begin isolated migration transaction: %v", tx.Error)
	}
	defer tx.Rollback()

	schema := fmt.Sprintf("post_quote_count_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatalf("set isolated migration schema: %v", err)
	}
	if err := tx.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatalf("enable pgvector: %v", err)
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("create initial schema: %v", err)
	}

	user := models.User{Username: "quote-count-migration-" + uuid.NewString(), Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatalf("create migration fixture user: %v", err)
	}
	target := models.Post{AuthorID: user.ID, Content: "target", Visibility: "public", QuoteCount: 41}
	activeA := models.Post{AuthorID: user.ID, Content: "active quote a", Visibility: "public"}
	activeB := models.Post{AuthorID: user.ID, Content: "active quote b", Visibility: "public"}
	deleted := models.Post{AuthorID: user.ID, Content: "deleted quote", Visibility: "public"}
	for _, post := range []*models.Post{&target, &activeA, &activeB, &deleted} {
		if post != &target {
			post.QuotePostID = &target.ID
		}
		if err := tx.Create(post).Error; err != nil {
			t.Fatalf("create migration fixture post: %v", err)
		}
	}
	if err := tx.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft-delete migration fixture Quote: %v", err)
	}
	unquotedTarget := models.Post{AuthorID: user.ID, Content: "unquoted target", Visibility: "public"}
	softDeletedTarget := models.Post{AuthorID: user.ID, Content: "soft-deleted target", Visibility: "public"}
	quoteOfDeletedTarget := models.Post{
		AuthorID: user.ID, Content: "active quote of deleted target", Visibility: "public", QuotePostID: &softDeletedTarget.ID,
	}
	quoteOfQuote := models.Post{
		AuthorID: user.ID, Content: "quote of quote", Visibility: "public", QuotePostID: &activeA.ID,
	}
	for _, post := range []*models.Post{&unquotedTarget, &softDeletedTarget, &quoteOfDeletedTarget, &quoteOfQuote} {
		if err := tx.Create(post).Error; err != nil {
			t.Fatalf("create migration fixture post: %v", err)
		}
	}
	if err := tx.Delete(&softDeletedTarget).Error; err != nil {
		t.Fatalf("soft-delete migration fixture target: %v", err)
	}
	if err := tx.Model(&models.RuntimeSchemaState{}).Where("id = ?", runtimeSchemaStateID).
		Updates(map[string]any{"current_version": 12, "compatibility_floor": 12}).Error; err != nil {
		t.Fatalf("simulate schema 12 runtime contract: %v", err)
	}
	if err := tx.Exec("ALTER TABLE posts DROP CONSTRAINT chk_posts_quote_count_nonnegative").Error; err != nil {
		t.Fatalf("drop schema 13 quote constraint: %v", err)
	}
	if err := tx.Exec("ALTER TABLE posts DROP COLUMN quote_count").Error; err != nil {
		t.Fatalf("simulate schema 12 posts table: %v", err)
	}

	// Fail at schema publication, after the backfill, to exercise rollback and retry.
	if err := tx.Exec(`CREATE FUNCTION fail_quote_count_schema_publication()
RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'injected quote-count schema publication failure';
END;
$$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatalf("create migration failure function: %v", err)
	}
	if err := tx.Exec(`CREATE TRIGGER trg_fail_quote_count_schema_publication
BEFORE UPDATE ON runtime_schema_state
FOR EACH ROW EXECUTE FUNCTION fail_quote_count_schema_publication()`).Error; err != nil {
		t.Fatalf("create migration failure trigger: %v", err)
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err == nil ||
		!strings.Contains(err.Error(), "injected quote-count schema publication failure") {
		t.Fatalf("expected failure after backfill, got: %v", err)
	}
	var state models.RuntimeSchemaState
	if err := tx.First(&state, runtimeSchemaStateID).Error; err != nil {
		t.Fatalf("read schema state after rollback: %v", err)
	}
	if state.CurrentVersion != 12 || state.CompatibilityFloor != 12 {
		t.Fatalf("schema after rollback=%d/%d want=12/12", state.CurrentVersion, state.CompatibilityFloor)
	}
	if tx.Migrator().HasColumn(&models.Post{}, "quote_count") {
		t.Fatal("failed migration did not roll back the quote_count column")
	}
	if err := tx.Exec("DROP TRIGGER trg_fail_quote_count_schema_publication ON runtime_schema_state").Error; err != nil {
		t.Fatalf("remove migration failure trigger: %v", err)
	}

	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("upgrade schema 12: %v", err)
	}
	assertQuoteCountMigrationState(t, tx, target.ID, 2)
	assertQuoteCountMigrationState(t, tx, unquotedTarget.ID, 0)
	assertQuoteCountMigrationState(t, tx, softDeletedTarget.ID, 1)
	assertQuoteCountMigrationState(t, tx, activeA.ID, 1)
	assertQuoteCountColumnContract(t, tx)
	if err := tx.First(&state, runtimeSchemaStateID).Error; err != nil {
		t.Fatalf("read published schema state: %v", err)
	}
	if state.CurrentVersion != 13 || state.CompatibilityFloor != 13 {
		t.Fatalf("published runtime schema=%d/%d want=13/13", state.CurrentVersion, state.CompatibilityFloor)
	}

	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("rerun schema migration: %v", err)
	}
	assertQuoteCountMigrationState(t, tx, target.ID, 2)
	if err := tx.Model(&models.Post{}).Where("id = ?", target.ID).Update("quote_count", 7).Error; err != nil {
		t.Fatalf("set post-upgrade sentinel count: %v", err)
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("rerun current schema migration: %v", err)
	}
	assertQuoteCountMigrationState(t, tx, target.ID, 7)
}

func assertQuoteCountMigrationState(t *testing.T, db *gorm.DB, postID uint, want int64) {
	t.Helper()
	var post models.Post
	if err := db.Unscoped().First(&post, postID).Error; err != nil {
		t.Fatalf("load quote count target %d: %v", postID, err)
	}
	if post.QuoteCount != want {
		t.Fatalf("target %d quote_count=%d want=%d", postID, post.QuoteCount, want)
	}
}

func assertQuoteCountColumnContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	var column struct {
		Nullable string `gorm:"column:is_nullable"`
		Default  string `gorm:"column:column_default"`
	}
	if err := db.Raw(`SELECT is_nullable, COALESCE(column_default, '') AS column_default
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'posts' AND column_name = 'quote_count'`).Scan(&column).Error; err != nil {
		t.Fatalf("read quote_count column contract: %v", err)
	}
	if column.Nullable != "NO" || !strings.Contains(column.Default, "0") {
		t.Fatalf("quote_count nullable=%q default=%q", column.Nullable, column.Default)
	}
	var constraint string
	if err := db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid = 'posts'::regclass AND conname = 'chk_posts_quote_count_nonnegative'`).Scan(&constraint).Error; err != nil {
		t.Fatalf("read quote_count constraint: %v", err)
	}
	definition := strings.ToLower(constraint)
	if !strings.Contains(definition, "quote_count") || !strings.Contains(definition, ">=") || !strings.Contains(definition, "0") {
		t.Fatalf("quote_count constraint definition=%q", constraint)
	}
}
