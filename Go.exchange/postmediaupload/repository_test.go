package postmediaupload

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeleteAfterObjectCleanupOnlyTargetsUploadingLeases(t *testing.T) {
	var queries bytes.Buffer
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger: logger.New(log.New(&queries, "", 0), logger.Config{
			LogLevel: logger.Info,
			Colorful: false,
		}),
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}

	err = DeleteAfterObjectCleanup(context.Background(), db, "media-123", 42)
	if !errors.Is(err, ErrUploadUnavailable) {
		t.Fatalf("dry-run delete error=%v want %v", err, ErrUploadUnavailable)
	}

	query := queries.String()
	if !strings.Contains(query, `AND status = 'uploading'`) {
		t.Fatalf("cleanup delete is not restricted to uploading leases: %s", query)
	}
	if strings.Contains(query, "status IN") || strings.Contains(query, "'uploaded'") {
		t.Fatalf("cleanup delete can match an uploaded lease: %s", query)
	}
}
