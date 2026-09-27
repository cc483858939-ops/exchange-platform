package postmediaupload

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"Go.exchange/models"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestClaimCleanupBatchUsesSkipLockedOrderedBoundedQuery(t *testing.T) {
	var queries bytes.Buffer
	db := cleanupDryRunDB(t, &queries)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var uploads []models.PostMediaUpload
	if err := cleanupCandidatesQuery(db, now, 7).Find(&uploads).Error; err != nil {
		t.Fatal(err)
	}
	query := strings.ToLower(queries.String())
	for _, part := range []string{"for update skip locked", "order by cleanup_after asc, media_id asc", "limit 7", "cleanup_after <=", "status in"} {
		if !strings.Contains(query, part) {
			t.Fatalf("claim SQL is missing %q: %s", part, query)
		}
	}
}

func TestCleanupMutationRejectsStaleTokens(t *testing.T) {
	var queries bytes.Buffer
	db := cleanupDryRunDB(t, &queries)
	claim := CleanupClaim{MediaID: "550e8400-e29b-41d4-a716-446655440000", ClaimToken: uuid.NewString()}
	if err := CompleteCleanup(context.Background(), db, claim); !errors.Is(err, ErrStaleCleanupClaim) {
		t.Fatalf("dry-run completion error=%v want stale token", err)
	}
	if err := ScheduleCleanupRetry(context.Background(), db, claim, time.Now().Add(time.Hour), "storage_delete_failed"); !errors.Is(err, ErrStaleCleanupClaim) {
		t.Fatalf("dry-run retry error=%v want stale token", err)
	}
	query := strings.ToLower(queries.String())
	if !strings.Contains(query, "cleanup_claim_token") || !strings.Contains(query, "cleanup_pending") {
		t.Fatalf("cleanup mutations are missing state/token fences: %s", query)
	}
}

func TestBoundedCleanupErrorPreservesUTF8AndCapsBytes(t *testing.T) {
	got := boundedCleanupError(CleanupErrorStorageDeleteFailure)
	if len(got) > MaxCleanupErrorBytes {
		t.Fatalf("cleanup error is %d bytes, max %d", len(got), MaxCleanupErrorBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("cleanup error is not valid UTF-8: %q", got)
	}
	if got := boundedCleanupError("https://private.invalid/signed?credential=secret\nstacktrace"); got != "storage_cleanup_failed" {
		t.Fatalf("unsafe raw storage error was persisted: %q", got)
	}
	truncated := truncateCleanupError(strings.Repeat("é", 700))
	if len(truncated) > MaxCleanupErrorBytes || !utf8.ValidString(truncated) {
		t.Fatalf("truncated error bytes=%d valid=%t", len(truncated), utf8.ValidString(truncated))
	}
}

func cleanupDryRunDB(t *testing.T, output *bytes.Buffer) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger: logger.New(log.New(output, "", 0), logger.Config{
			LogLevel: logger.Info,
			Colorful: false,
		}),
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}
	return db
}
