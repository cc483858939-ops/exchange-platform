package initialize

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/models"
	"Go.exchange/postmedia"
	"Go.exchange/postmediacleanup"
	"Go.exchange/postmediaupload"
	"github.com/google/uuid"
)

func TestPostMediaDeletionMigrationAndClaimsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	schema := "media_deletion_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}, &models.Post{}, &models.PostMedia{}, &models.PostMediaCleanup{}); err != nil {
		t.Fatal(err)
	}
	owner := models.User{Username: "media-deletion-owner", Password: "test"}
	if err := tx.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	deleted := models.Post{AuthorID: owner.ID, Content: "deleted", Visibility: "public"}
	live := models.Post{AuthorID: owner.ID, Content: "shared", Visibility: "public"}
	if err := tx.Create(&[]*models.Post{&deleted, &live}).Error; err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	paths, err := postmedia.BuildUserV1ObjectPaths(owner.ID, id, ".webp", ".png")
	if err != nil {
		t.Fatal(err)
	}
	for _, postID := range []uint{deleted.ID, live.ID} {
		if err := tx.Create(&models.PostMedia{PostID: postID, MediaType: "image", URL: postmedia.PublicURL(paths.MediumObjectKey), LargeURL: postmedia.PublicURL(paths.LargeObjectKey), Width: 10, Height: 10, Position: 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyPostMediaDeletionSchema(tx); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Model(&models.PostMediaCleanup{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("schema initialization created cleanup work: count=%d err=%v", count, err)
	}
	job := models.PostMediaCleanup{
		MediaID: id, OwnerID: owner.ID, MediumObjectKey: paths.MediumObjectKey,
		LargeObjectKey: paths.LargeObjectKey, CreatedAt: time.Now().UTC(), CleanupAfter: time.Now().UTC(),
	}
	if err := tx.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_post_media_url", "idx_post_media_large_url", "idx_post_media_cleanup_due"} {
		var exists bool
		if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = ? AND indexname = ?)", schema, index).Scan(&exists).Error; err != nil || !exists {
			t.Fatalf("index %s exists=%t err=%v", index, exists, err)
		}
	}
	now := time.Now().UTC().Add(time.Second)
	claims, err := postmediacleanup.ClaimBatch(context.Background(), tx, now, time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	allowed, err := postmediacleanup.Unreferenced(context.Background(), tx, claims[0])
	if err != nil || allowed {
		t.Fatalf("shared image deletable=%t err=%v", allowed, err)
	}
	if err := postmediacleanup.Retry(context.Background(), tx, claims[0], now.Add(time.Minute), "media_still_referenced"); err != nil {
		t.Fatal(err)
	}
	if err := postmediacleanup.Complete(context.Background(), tx, claims[0]); !errors.Is(err, postmediaupload.ErrStaleCleanupClaim) {
		t.Fatalf("released token completed=%v", err)
	}
	if err := tx.Delete(&live).Error; err != nil {
		t.Fatal(err)
	}
	claims, err = postmediacleanup.ClaimBatch(context.Background(), tx, now.Add(2*time.Minute), time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("retry claims=%v err=%v", claims, err)
	}
	if allowed, err := postmediacleanup.Unreferenced(context.Background(), tx, claims[0]); err != nil || !allowed {
		t.Fatalf("deleted references retained=%t err=%v", !allowed, err)
	}
	if err := applyPostMediaDeletionSchema(tx); err != nil {
		t.Fatal(err)
	}
	var preserved models.PostMediaCleanup
	if err := tx.First(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.Attempts != 2 || preserved.ClaimToken == nil || *preserved.ClaimToken != claims[0].ClaimToken {
		t.Fatalf("migration reset live claim: %+v", preserved)
	}
	if err := postmediacleanup.Complete(context.Background(), tx, claims[0]); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := tx.Model(&models.PostMediaCleanup{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	if err := applyPostMediaDeletionSchema(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Model(&models.PostMediaCleanup{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("completed work re-enqueued: remaining=%d err=%v", remaining, err)
	}
}
