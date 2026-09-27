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
		"cleanup_claim_token", "cleanup_claimed_at", "cleanup_attempts", "last_cleanup_error",
	} {
		if _, exists := columnNullability[required]; !exists {
			t.Fatalf("post_media_uploads is missing required column %q: %v", required, columnNullability)
		}
	}
	if columnNullability["media_id"] != "NO" || columnNullability["owner_id"] != "NO" || columnNullability["status"] != "NO" || columnNullability["cleanup_after"] != "NO" || columnNullability["cleanup_attempts"] != "NO" {
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
		"post_media_uploads_pkey":                             {"primarykey(media_id)"},
		"fk_post_media_uploads_owner":                         {"foreignkey(owner_id)", "referencesusers(id)", "ondeleterestrict"},
		"chk_post_media_uploads_owner_positive":               {"owner_id>0"},
		"chk_post_media_uploads_status":                       {"status", "uploading", "uploaded", "cleanup_pending"},
		"chk_post_media_uploads_uploaded_shape":               {"uploaded_atisnotnull", "width>0", "height>0", "cleanup_after>=uploaded_at"},
		"chk_post_media_uploads_cleanup_attempts_nonnegative": {"cleanup_attempts>=0"},
		"chk_post_media_uploads_cleanup_claim_shape":          {"cleanup_claim_token", "cleanup_claimed_at", "cleanup_pending"},
		"chk_post_media_uploads_cleanup_error_size":           {"octet_length(last_cleanup_error)<=1024"},
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
	var gcIndexDefinition string
	if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'post_media_uploads' AND indexname = 'idx_post_media_uploads_gc_eligible'").Scan(&gcIndexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	normalizedGCIndex := strings.ToLower(strings.Join(strings.Fields(gcIndexDefinition), " "))
	for _, part := range []string{"(cleanup_after, media_id)", "uploading", "uploaded", "cleanup_pending"} {
		if !strings.Contains(normalizedGCIndex, part) {
			t.Fatalf("invalid Post media GC candidate index definition=%q missing %q", gcIndexDefinition, part)
		}
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
	mediaIDs := []string{mediaID}
	t.Cleanup(func() {
		db.Unscoped().Where("media_id IN ?", mediaIDs).Delete(&models.PostMediaUpload{})
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

	cleanupPending := upload
	cleanupPending.MediaID = uuid.NewString()
	cleanupPending.Status = postmediaupload.StatusCleanupPending
	cleanupPending.CleanupAttempts = 1
	cleanupPending.CleanupAfter = now.Add(time.Hour)
	mediaIDs = append(mediaIDs, cleanupPending.MediaID)
	if err := db.Create(&cleanupPending).Error; err != nil {
		t.Fatalf("cleanup_pending retry state was rejected: %v", err)
	}
	activeClaim := upload
	activeClaim.MediaID = uuid.NewString()
	activeClaim.Status = postmediaupload.StatusCleanupPending
	activeClaim.CleanupAttempts = 1
	activeClaim.CleanupAfter = now.Add(time.Hour)
	claimToken := uuid.NewString()
	activeClaim.CleanupClaimToken = &claimToken
	claimedAt := now.Add(time.Minute)
	activeClaim.CleanupClaimedAt = &claimedAt
	mediaIDs = append(mediaIDs, activeClaim.MediaID)
	if err := db.Create(&activeClaim).Error; err != nil {
		t.Fatalf("active cleanup claim state was rejected: %v", err)
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

func TestDeleteAfterObjectCleanupStateFenceIntegration(t *testing.T) {
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

	owner := models.User{Username: "post-media-cleanup-guard-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var mediaIDs []string
	t.Cleanup(func() {
		if len(mediaIDs) > 0 {
			if err := db.Unscoped().Where("media_id IN ?", mediaIDs).Delete(&models.PostMediaUpload{}).Error; err != nil {
				t.Errorf("clean up Post media upload fixtures: %v", err)
			}
		}
		if err := db.Unscoped().Delete(&models.User{}, owner.ID).Error; err != nil {
			t.Errorf("clean up Post media upload test owner: %v", err)
		}
	})

	newUploadingLease := func() models.PostMediaUpload {
		mediaID := uuid.NewString()
		now := time.Now().UTC().Truncate(time.Microsecond)
		return models.PostMediaUpload{
			MediaID:           mediaID,
			OwnerID:           owner.ID,
			Status:            postmediaupload.StatusUploading,
			OriginalObjectKey: "post-media/users/v1/1/" + mediaID + "/original.jpg",
			MediumObjectKey:   "post-media/users/v1/1/" + mediaID + "/medium.jpg",
			LargeObjectKey:    "post-media/users/v1/1/" + mediaID + "/large.jpg",
			ManifestObjectKey: "post-media/users/v1/1/" + mediaID + "/manifest.json",
			MediumURL:         "/api/files/post-media/users/v1/1/" + mediaID + "/medium.jpg",
			LargeURL:          "/api/files/post-media/users/v1/1/" + mediaID + "/large.jpg",
			Width:             1200,
			Height:            800,
			CreatedAt:         now,
			CleanupAfter:      now.Add(postmediaupload.DefaultUploadTimeout),
		}
	}
	createLease := func(upload models.PostMediaUpload) {
		t.Helper()
		mediaIDs = append(mediaIDs, upload.MediaID)
		if err := postmediaupload.CreateUploading(context.Background(), db, upload); err != nil {
			t.Fatalf("create uploading lease %q: %v", upload.MediaID, err)
		}
	}

	t.Run("uploaded lease is protected with metadata intact", func(t *testing.T) {
		upload := newUploadingLease()
		createLease(upload)
		uploadedAt := upload.CreatedAt.Add(time.Minute)
		cleanupAfter := uploadedAt.Add(postmediaupload.DefaultGracePeriod)
		if err := postmediaupload.MarkUploaded(context.Background(), db, upload.MediaID, owner.ID, uploadedAt, cleanupAfter); err != nil {
			t.Fatalf("mark lease uploaded: %v", err)
		}

		var before models.PostMediaUpload
		if err := db.Where("media_id = ?", upload.MediaID).Take(&before).Error; err != nil {
			t.Fatalf("load uploaded lease before cleanup attempt: %v", err)
		}
		if before.MediaID != upload.MediaID || before.OwnerID != owner.ID || before.Status != postmediaupload.StatusUploaded || before.UploadedAt == nil || !before.UploadedAt.Equal(uploadedAt) || !before.CleanupAfter.Equal(cleanupAfter) {
			t.Fatalf("uploaded lease precondition failed: %+v", before)
		}

		if err := postmediaupload.DeleteAfterObjectCleanup(context.Background(), db, upload.MediaID, owner.ID); !errors.Is(err, postmediaupload.ErrUploadUnavailable) {
			t.Fatalf("cleanup deletion error=%v, want ErrUploadUnavailable", err)
		}

		var after models.PostMediaUpload
		if err := db.Where("media_id = ?", upload.MediaID).Take(&after).Error; err != nil {
			t.Fatalf("uploaded lease disappeared after cleanup attempt: %v", err)
		}
		if after.MediaID != before.MediaID || after.OwnerID != before.OwnerID || after.Status != postmediaupload.StatusUploaded || after.UploadedAt == nil || !after.UploadedAt.Equal(*before.UploadedAt) || !after.CleanupAfter.Equal(before.CleanupAfter) {
			t.Fatalf("uploaded lease metadata changed after rejected cleanup: before=%+v after=%+v", before, after)
		}
	})

	t.Run("uploading lease is deletable", func(t *testing.T) {
		upload := newUploadingLease()
		createLease(upload)

		if err := postmediaupload.DeleteAfterObjectCleanup(context.Background(), db, upload.MediaID, owner.ID); err != nil {
			t.Fatalf("delete uploading lease: %v", err)
		}
		var stored models.PostMediaUpload
		if err := db.Where("media_id = ?", upload.MediaID).Take(&stored).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("uploading lease lookup error=%v, want row to be absent", err)
		}
	})

	t.Run("wrong owner cannot delete uploading lease", func(t *testing.T) {
		upload := newUploadingLease()
		createLease(upload)

		wrongOwnerID := owner.ID + 1
		if wrongOwnerID == owner.ID {
			t.Fatal("could not construct distinct wrong owner ID")
		}
		if err := postmediaupload.DeleteAfterObjectCleanup(context.Background(), db, upload.MediaID, wrongOwnerID); !errors.Is(err, postmediaupload.ErrUploadUnavailable) {
			t.Fatalf("wrong-owner cleanup error=%v, want ErrUploadUnavailable", err)
		}
		var stored models.PostMediaUpload
		if err := db.Where("media_id = ?", upload.MediaID).Take(&stored).Error; err != nil {
			t.Fatalf("wrong-owner cleanup removed lease: %v", err)
		}
		if stored.MediaID != upload.MediaID || stored.OwnerID != owner.ID || stored.Status != postmediaupload.StatusUploading {
			t.Fatalf("wrong-owner cleanup changed lease: %+v", stored)
		}
	})

	t.Run("missing lease is unavailable", func(t *testing.T) {
		if err := postmediaupload.DeleteAfterObjectCleanup(context.Background(), db, uuid.NewString(), owner.ID); !errors.Is(err, postmediaupload.ErrUploadUnavailable) {
			t.Fatalf("missing-lease cleanup error=%v, want ErrUploadUnavailable", err)
		}
	})
}
