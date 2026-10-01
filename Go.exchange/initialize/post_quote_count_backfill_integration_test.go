package initialize

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBackfillPostQuoteCountsUpdatesOnlyQuotedTargetsIntegration(t *testing.T) {
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
		t.Fatalf("begin isolated backfill transaction: %v", tx.Error)
	}
	defer tx.Rollback()

	schema := fmt.Sprintf("post_quote_count_backfill_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatalf("set isolated backfill schema: %v", err)
	}
	if err := tx.AutoMigrate(&models.User{}, &models.Post{}); err != nil {
		t.Fatalf("create backfill fixture tables: %v", err)
	}
	user := models.User{Username: "quote-count-backfill", Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatalf("create backfill fixture user: %v", err)
	}
	createPost := func(content string, target *models.Post) models.Post {
		t.Helper()
		post := models.Post{AuthorID: user.ID, Content: content, Visibility: "public"}
		if target != nil {
			post.QuotePostID = &target.ID
		}
		if err := tx.Create(&post).Error; err != nil {
			t.Fatalf("create backfill fixture post %q: %v", content, err)
		}
		return post
	}
	targetA := createPost("target A", nil)
	targetB := createPost("target B", nil)
	unquoted1 := createPost("unquoted 1", nil)
	unquoted2 := createPost("unquoted 2 (only deleted incoming quote)", nil)
	quoteA1 := createPost("active quote A1", &targetA)
	quoteA2 := createPost("active quote A2", &targetA)
	quoteB1 := createPost("active quote B1", &targetB)
	quoteOfQuote := createPost("quote of quote A1", &quoteA1)
	deletedQuote := createPost("deleted quote of A", &targetA)
	deletedOnlyQuote := createPost("deleted quote of unquoted 2", &unquoted2)
	for _, post := range []*models.Post{&targetB, &deletedQuote, &deletedOnlyQuote} {
		if err := tx.Delete(post).Error; err != nil {
			t.Fatalf("soft-delete backfill fixture post %d: %v", post.ID, err)
		}
	}
	// The helper counts canonical edges independently of visibility or author state.
	if err := tx.Model(&quoteA2).Update("visibility", "private").Error; err != nil {
		t.Fatalf("set fixture Quote visibility: %v", err)
	}
	if err := tx.Delete(&user).Error; err != nil {
		t.Fatalf("soft-delete fixture author: %v", err)
	}
	posts := []*models.Post{
		&targetA, &targetB, &unquoted1, &unquoted2, &quoteA1, &quoteA2,
		&quoteB1, &quoteOfQuote, &deletedQuote, &deletedOnlyQuote,
	}
	for _, post := range posts {
		assertQuoteCountMigrationState(t, tx, post.ID, 0)
	}

	for _, statement := range []string{
		`CREATE TABLE quote_count_update_audit (post_id bigint NOT NULL)`,
		`CREATE FUNCTION audit_quote_count_update()
RETURNS trigger AS $$
BEGIN
	INSERT INTO quote_count_update_audit(post_id) VALUES (NEW.id);
	RETURN NEW;
END;
$$ LANGUAGE plpgsql`,
		`CREATE TRIGGER trg_audit_quote_count_update
AFTER UPDATE OF quote_count ON posts
FOR EACH ROW EXECUTE FUNCTION audit_quote_count_update()`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatalf("install quote count update audit: %v", err)
		}
	}
	if err := backfillPostQuoteCounts(tx); err != nil {
		t.Fatalf("backfill quote counts: %v", err)
	}
	wantCounts := map[uint]int64{targetA.ID: 2, targetB.ID: 1, quoteA1.ID: 1}
	for _, post := range posts {
		assertQuoteCountMigrationState(t, tx, post.ID, wantCounts[post.ID])
	}

	var updates []struct {
		PostID      uint
		UpdateCount int64
	}
	if err := tx.Raw(`SELECT post_id, COUNT(*) AS update_count
FROM quote_count_update_audit GROUP BY post_id`).Scan(&updates).Error; err != nil {
		t.Fatalf("read quote count update audit: %v", err)
	}
	if len(updates) != len(wantCounts) {
		t.Fatalf("updated posts=%+v want exactly targets=%v", updates, wantCounts)
	}
	for _, update := range updates {
		if _, ok := wantCounts[update.PostID]; !ok {
			t.Errorf("post %d without active incoming Quotes was updated", update.PostID)
		}
		if update.UpdateCount != 1 {
			t.Errorf("post %d updated %d times, want 1", update.PostID, update.UpdateCount)
		}
	}
}
