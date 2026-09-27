package initialize

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"
	"Go.exchange/postmediaupload"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostMediaUploadMigrationSchemaIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSchema(context.Background(), db, DefaultSchemaValidationOptions()); err != nil {
		t.Fatalf("post-migration runtime schema check failed: %v", err)
	}

	var columns []struct {
		Name     string `gorm:"column:column_name"`
		Nullable string `gorm:"column:is_nullable"`
	}
	if err := db.Raw(`
SELECT column_name, is_nullable
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'post_media_uploads'
`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	columnNullability := make(map[string]string, len(columns))
	for _, column := range columns {
		columnNullability[column.Name] = column.Nullable
	}
	for _, required := range []string{
		"media_id", "owner_id", "status", "original_object_key", "medium_object_key", "large_object_key",
		"manifest_object_key", "medium_url", "large_url", "width", "height", "created_at", "uploaded_at", "cleanup_after",
	} {
		if _, exists := columnNullability[required]; !exists {
			t.Fatalf("post_media_uploads is missing required column %q: %v", required, columnNullability)
		}
	}
	if columnNullability["media_id"] != "NO" || columnNullability["owner_id"] != "NO" || columnNullability["status"] != "NO" || columnNullability["cleanup_after"] != "NO" {
		t.Fatalf("required lifecycle columns must be NOT NULL: %v", columnNullability)
	}

	var constraints []struct {
		Name       string `gorm:"column:conname"`
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT conname, pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = 'post_media_uploads'::regclass
`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	definitions := make(map[string]string, len(constraints))
	for _, constraint := range constraints {
		definitions[constraint.Name] = strings.ToLower(strings.Join(strings.Fields(constraint.Definition), ""))
	}
	for name, parts := range map[string][]string{
		"post_media_uploads_pkey":               {"primarykey(media_id)"},
		"fk_post_media_uploads_owner":           {"foreignkey(owner_id)", "referencesusers(id)", "ondeleterestrict"},
		"chk_post_media_uploads_owner_positive": {"owner_id>0"},
		"chk_post_media_uploads_status":         {"status", "uploading", "uploaded"},
		"chk_post_media_uploads_uploaded_shape": {"uploaded_atisnotnull", "width>0", "height>0", "cleanup_after>=uploaded_at"},
	} {
		definition, exists := definitions[name]
		if !exists {
			t.Fatalf("missing Post media upload constraint %q: %v", name, definitions)
		}
		for _, part := range parts {
			if !strings.Contains(definition, part) {
				t.Fatalf("constraint %q definition=%q missing %q", name, definition, part)
			}
		}
	}

	var indexDefinition string
	if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'post_media_uploads' AND indexname = 'idx_post_media_uploads_status_cleanup_after'").Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	if normalized := strings.ToLower(strings.ReplaceAll(indexDefinition, " ", "")); !strings.Contains(normalized, "(status,cleanup_after)") {
		t.Fatalf("invalid Post media cleanup index definition=%q", indexDefinition)
	}

	owner := models.User{Username: "post-media-upload-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	mediaID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	upload := models.PostMediaUpload{
		MediaID: mediaID, OwnerID: owner.ID, Status: postmediaupload.StatusUploading,
		OriginalObjectKey: "post-media/users/v1/1/" + mediaID + "/original.jpg",
		MediumObjectKey:   "post-media/users/v1/1/" + mediaID + "/medium.jpg",
		LargeObjectKey:    "post-media/users/v1/1/" + mediaID + "/large.jpg",
		ManifestObjectKey: "post-media/users/v1/1/" + mediaID + "/manifest.json",
		MediumURL:         "/api/files/post-media/users/v1/1/" + mediaID + "/medium.jpg",
		LargeURL:          "/api/files/post-media/users/v1/1/" + mediaID + "/large.jpg",
		Width:             1200, Height: 800, CreatedAt: now, CleanupAfter: now.Add(postmediaupload.DefaultUploadTimeout),
	}
	if err := db.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{})
		db.Unscoped().Delete(&models.User{}, owner.ID)
	})
	uploadedAt := now.Add(time.Minute)
	cleanupAfter := uploadedAt.Add(postmediaupload.DefaultGracePeriod)
	if err := postmediaupload.MarkUploaded(context.Background(), db, mediaID, owner.ID, uploadedAt, cleanupAfter); err != nil {
		t.Fatal(err)
	}
	if err := postmediaupload.MarkUploaded(context.Background(), db, mediaID, owner.ID, uploadedAt, cleanupAfter); !errors.Is(err, postmediaupload.ErrUploadUnavailable) {
		t.Fatalf("second upload transition error=%v, want compare-and-swap rejection", err)
	}
	var stored models.PostMediaUpload
	if err := db.Where("media_id = ?", mediaID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != postmediaupload.StatusUploaded || stored.UploadedAt == nil || !stored.UploadedAt.Equal(uploadedAt) || !stored.CleanupAfter.Equal(cleanupAfter) {
		t.Fatalf("stored uploaded lease=%+v", stored)
	}

	invalidStatus := upload
	invalidStatus.MediaID = uuid.NewString()
	invalidStatus.Status = "bound"
	if err := db.Create(&invalidStatus).Error; err == nil {
		t.Fatal("status CHECK accepted an unsupported lifecycle state")
	} else {
		requirePostgresCheckViolation(t, "unsupported Post media upload state", err, "chk_post_media_uploads_status")
	}
	invalidOwner := upload
	invalidOwner.MediaID = uuid.NewString()
	invalidOwner.OwnerID = owner.ID + 1000000
	if err := db.Create(&invalidOwner).Error; err == nil {
		t.Fatal("owner foreign key accepted a missing user")
	} else {
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23503" || postgresError.ConstraintName != "fk_post_media_uploads_owner" {
			t.Fatalf("invalid owner error=%v", err)
		}
	}
}
