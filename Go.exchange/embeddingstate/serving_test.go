package embeddingstate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestServingStateRejectsMissingInputs(t *testing.T) {
	if _, err := LoadServingVersion(nil, nil); err == nil {
		t.Fatal("nil context must fail")
	}
	if _, err := LoadServingVersion(context.Background(), nil); err == nil {
		t.Fatal("nil database must fail")
	}
	if err := SetServingVersion(nil, nil, "v2"); err == nil {
		t.Fatal("nil context must fail")
	}
	if err := SetServingVersion(context.Background(), nil, "v2"); err == nil {
		t.Fatal("nil database must fail")
	}
	if err := SetServingVersion(context.Background(), nil, "  "); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Fatalf("blank version error=%v", err)
	}
}

func TestServingStateLoadSetAndMissingRowIntegration(t *testing.T) {
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
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin serving-state test transaction: %v", tx.Error)
	}
	t.Cleanup(func() {
		_ = tx.Rollback().Error
		_ = sqlDB.Close()
	})
	if err := tx.Exec(`CREATE TEMP TABLE embedding_serving_state (
		id BIGINT PRIMARY KEY,
		serving_version VARCHAR(64) NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create temporary serving-state table: %v", err)
	}
	if err := tx.Exec("INSERT INTO embedding_serving_state (id, serving_version, updated_at) VALUES (1, ?, NOW())", " post_embedding_v1 ").Error; err != nil {
		t.Fatalf("insert serving-state row: %v", err)
	}

	ctx := context.Background()
	if got, err := LoadServingVersion(ctx, tx); err != nil || got != "post_embedding_v1" {
		t.Fatalf("initial serving version=%q err=%v", got, err)
	}
	if err := SetServingVersion(ctx, tx, " post_embedding_v2 "); err != nil {
		t.Fatalf("set serving version: %v", err)
	}
	if got, err := LoadServingVersion(ctx, tx); err != nil || got != "post_embedding_v2" {
		t.Fatalf("updated serving version=%q err=%v", got, err)
	}
	if err := SetServingVersion(ctx, tx, " \t "); err == nil {
		t.Fatal("blank serving version unexpectedly succeeded")
	}
	if err := tx.Exec("UPDATE embedding_serving_state SET serving_version = '   ' WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServingVersion(ctx, tx); err == nil {
		t.Fatal("blank database serving version unexpectedly loaded")
	}
	if err := tx.Exec("DELETE FROM embedding_serving_state WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServingVersion(ctx, tx); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing singleton error=%v", err)
	}
	if err := SetServingVersion(ctx, tx, "post_embedding_v2"); err == nil {
		t.Fatal("setting a missing singleton row unexpectedly succeeded")
	}
}
