package initialize

import (
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/models"
)

func TestPostLikeCleanupMigrationPreservesPendingWork(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN")
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
	schema := fmt.Sprintf("like_delete_migration_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "schema17", Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	deleted := models.Post{AuthorID: user.ID, Content: "deleted", Visibility: "public"}
	active := models.Post{AuthorID: user.ID, Content: "active", Visibility: "public"}
	for _, post := range []*models.Post{&deleted, &active} {
		if err := tx.Create(post).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	retryAfter := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	pending := models.PostLikeCleanup{PostID: deleted.ID, RetryAfter: retryAfter}
	if err := tx.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	var work []models.PostLikeCleanup
	if err := tx.Find(&work).Error; err != nil || len(work) != 1 || work[0].PostID != deleted.ID || !work[0].RetryAfter.Equal(retryAfter) {
		t.Fatalf("pending work changed=%+v err=%v", work, err)
	}
	if err := tx.Delete(&work).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Model(&models.PostLikeCleanup{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("completed work re-enqueued: count=%d err=%v", count, err)
	}
}
