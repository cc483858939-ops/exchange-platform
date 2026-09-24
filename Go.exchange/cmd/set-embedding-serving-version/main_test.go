package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUpdateServingVersionRejectsBlankVersion(t *testing.T) {
	if err := updateServingVersion(context.Background(), nil, " \t ", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Fatalf("blank version error=%v", err)
	}
}

func TestUpdateServingVersionPrintsOldAndNewValuesIntegration(t *testing.T) {
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
		t.Fatalf("begin command test transaction: %v", tx.Error)
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
	if err := tx.Exec("INSERT INTO embedding_serving_state (id, serving_version, updated_at) VALUES (1, 'post_embedding_v1', NOW())").Error; err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := updateServingVersion(context.Background(), tx, " post_embedding_v2 ", &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "embedding serving version updated:\nold=post_embedding_v1\nnew=post_embedding_v2\n"; got != want {
		t.Fatalf("command output=%q want=%q", got, want)
	}
}
