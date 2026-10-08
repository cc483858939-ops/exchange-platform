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

func TestPostQuoteCountCurrentSchemaRollbackAndRetryIntegration(t *testing.T) {
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
		t.Fatal(err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	assertQuoteCountColumnContract(t, tx)
	user := models.User{Username: "quote-count-migration-" + uuid.NewString(), Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	target := models.Post{AuthorID: user.ID, Content: "target", Visibility: "public"}
	if err := tx.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	assertQuoteCountMigrationState(t, tx, target.ID, 0)
	quote := models.Post{AuthorID: user.ID, Content: "quote", Visibility: "public", QuotePostID: &target.ID}
	if err := tx.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Model(&target).Update("quote_count", 7).Error; err != nil {
		t.Fatal(err)
	}

	// A failure at publication must roll back repairs to the current schema.
	for _, statement := range []string{
		"ALTER TABLE posts DROP CONSTRAINT chk_posts_quote_count_nonnegative",
		`CREATE FUNCTION fail_quote_count_schema_publication() RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'injected quote-count schema publication failure';
END;
$$ LANGUAGE plpgsql`,
		`CREATE TRIGGER trg_fail_quote_count_schema_publication BEFORE UPDATE ON runtime_schema_state
FOR EACH ROW EXECUTE FUNCTION fail_quote_count_schema_publication()`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := RunMigrationsWithDB(context.Background(), tx); err == nil ||
		!strings.Contains(err.Error(), "injected quote-count schema publication failure") {
		t.Fatalf("expected publication failure, got: %v", err)
	}
	if tx.Migrator().HasConstraint(&models.Post{}, "chk_posts_quote_count_nonnegative") {
		t.Fatal("failed migration left a partially applied constraint")
	}
	assertQuoteCountMigrationState(t, tx, target.ID, 7)
	if err := tx.Exec("DROP TRIGGER trg_fail_quote_count_schema_publication ON runtime_schema_state").Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := RunMigrationsWithDB(context.Background(), tx); err != nil {
			t.Fatalf("repair/re-run current schema: %v", err)
		}
		assertQuoteCountColumnContract(t, tx)
		assertQuoteCountMigrationState(t, tx, target.ID, 7)
		assertQuoteCountMigrationState(t, tx, quote.ID, 0)
	}
	var state models.RuntimeSchemaState
	if err := tx.First(&state, runtimeSchemaStateID).Error; err != nil {
		t.Fatal(err)
	}
	if state.CurrentVersion != PublishedSchemaCurrentVersion || state.CompatibilityFloor != PublishedSchemaCompatibilityFloor {
		t.Fatalf("published runtime schema=%d/%d", state.CurrentVersion, state.CompatibilityFloor)
	}
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
