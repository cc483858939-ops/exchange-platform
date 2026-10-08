package initialize

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"
)

func TestConsumerInboxCurrentSchemaAndRepeatedMigrationIntegration(t *testing.T) {
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
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("inbox_id_migration_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + quoteIntegrationIdentifier(schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := setIntegrationSearchPath(tx, schema); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	processedAt := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	existingIDs := []string{"61aa0755-0605-4a15-802f-b5bc55cb8bbb", "post-view", "like-state:7:42:5", strings.Repeat("😀", 36)}
	for _, id := range existingIDs {
		if err := tx.Create(&models.ConsumerInbox{ConsumerName: "migration", EventID: id, ProcessedAt: processedAt}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
			t.Fatalf("re-run migration: %v", err)
		}
	}
	var width int
	if err := tx.Raw(`SELECT character_maximum_length FROM information_schema.columns
		WHERE table_schema = ? AND table_name = 'consumer_inboxes' AND column_name = 'event_id'`, schema).Scan(&width).Error; err != nil || width != 128 {
		t.Fatalf("event ID column width=%d err=%v", width, err)
	}
	var state models.RuntimeSchemaState
	if err := tx.First(&state).Error; err != nil || state.CurrentVersion != PublishedSchemaCurrentVersion || state.CompatibilityFloor != 14 {
		t.Fatalf("schema state=%+v err=%v", state, err)
	}
	var existing []models.ConsumerInbox
	if err := tx.Where("consumer_name = ?", "migration").Find(&existing).Error; err != nil {
		t.Fatal(err)
	}
	if len(existing) != len(existingIDs) {
		t.Fatalf("existing Inbox keys lost: %+v", existing)
	}
	for _, row := range existing {
		if !row.ProcessedAt.Equal(processedAt) {
			t.Fatalf("migration changed processing time: %+v", row)
		}
	}
	for _, id := range existingIDs {
		first, err := eventing.MarkInboxProcessed(tx, "migration", " \t"+id+"\n")
		if err != nil || first {
			t.Fatalf("existing key %q reprocessed: first=%t err=%v", id, first, err)
		}
	}
	longID := "like-state:100000000:100000000:100000000"
	boundID := strings.Repeat("a", 128)
	unicodeBoundID := strings.Repeat("界", 128)
	first, err := eventing.MarkInboxProcessedBatch(tx, "migration", []string{longID, " " + longID + " ", boundID, unicodeBoundID})
	if err != nil || len(first) != 3 {
		t.Fatalf("long/bounded first delivery=%v err=%v", first, err)
	}
	for _, id := range []string{longID, boundID, unicodeBoundID} {
		if _, exists := first[id]; !exists {
			t.Fatalf("canonical key missing from first deliveries: %q", id)
		}
	}
	first, err = eventing.MarkInboxProcessedBatch(tx, "migration", []string{" " + longID, boundID + "\t", unicodeBoundID})
	if err != nil || len(first) != 0 {
		t.Fatalf("replay inserted duplicate keys: %v err=%v", first, err)
	}
	if _, err := eventing.MarkInboxProcessedBatch(tx, "migration", []string{"must-not-insert", strings.Repeat("a", 129)}); err == nil {
		t.Fatal("oversized batch ID accepted")
	}
	if _, err := eventing.MarkInboxProcessed(tx, "migration", "bad\x00id"); err == nil {
		t.Fatal("NUL ID accepted")
	}
	// Invalid IDs must fail before SQL and leave the caller's transaction usable.
	var count int64
	if err := tx.Model(&models.ConsumerInbox{}).Where("consumer_name = ?", "migration").Count(&count).Error; err != nil || count != int64(len(existingIDs)+3) {
		t.Fatalf("Inbox count=%d err=%v want=%d", count, err, len(existingIDs)+3)
	}
}
