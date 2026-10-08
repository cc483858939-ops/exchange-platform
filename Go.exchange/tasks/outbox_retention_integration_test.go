package tasks

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
)

func TestOutboxRetentionBoundedDeletionAndCatchup(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.WithContext(context.Background()).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE TEMP TABLE outbox_events (id uuid PRIMARY KEY, created_at timestamptz NOT NULL, message jsonb NOT NULL)`,
		`CREATE INDEX ON outbox_events (created_at ASC, id ASC)`,
		`INSERT INTO outbox_events SELECT md5(id::text)::uuid, '2026-01-01'::timestamptz+id*interval '1 second', jsonb_build_object('payload', repeat('x',1024)) FROM generate_series(1,24000) id`,
		`INSERT INTO outbox_events VALUES (md5('boundary')::uuid,'2026-01-02','{}'),(md5('recent')::uuid,'2026-01-03','{}')`,
		`ANALYZE outbox_events`,
	} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	var deleted int64
	for batch, expected := range []int64{5000, 5000, 5000, 5000, 4000, 0} {
		started := time.Now()
		rows, err := deleteRetainedOutboxRows(tx, cutoff, 5000)
		t.Logf("batch=%d rows=%d elapsed=%s (temporary table, excludes CDC/WAL capacity)", batch, rows, time.Since(started))
		if err != nil || rows != expected {
			t.Fatalf("batch=%d rows=%d want=%d err=%v", batch, rows, expected, err)
		}
		deleted += rows
	}
	var remaining int64
	if err := tx.Table("outbox_events").Count(&remaining).Error; err != nil || remaining != 2 || deleted != 24000 {
		t.Fatalf("remaining=%d deleted=%d err=%v", remaining, deleted, err)
	}
	var premature int64
	if err := tx.Table("outbox_events").Where("created_at < ?", cutoff).Count(&premature).Error; err != nil || premature != 0 {
		t.Fatalf("expired=%d err=%v", premature, err)
	}
}
