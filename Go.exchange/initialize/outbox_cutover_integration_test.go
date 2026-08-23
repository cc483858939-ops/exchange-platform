package initialize

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestOutboxCutoverClassificationAndConfirmationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := "outbox_cutover_" + fmt.Sprintf("%d", os.Getpid())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error })
	var cdcResourceExists bool
	if err := db.Raw(`
SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'goexchange_outbox_pub')
   OR EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'goexchange_outbox_slot')`).Scan(&cdcResourceExists).Error; err != nil {
		t.Fatal(err)
	}
	if cdcResourceExists {
		t.Skip("legacy cutover resource guard requires an isolated database without the fixed CDC publication or slot")
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
CREATE TABLE outbox_events (
  id BIGSERIAL PRIMARY KEY,
  payload JSONB NOT NULL,
  published_at TIMESTAMPTZ,
  publish_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`).Error; err != nil {
			return err
		}
		state, err := DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaLegacy {
			return fmt.Errorf("legacy state=%s err=%v", state, err)
		}
		if err := cutoverLegacyOutboxTx(tx, false); err == nil {
			return fmt.Errorf("unconfirmed cutover unexpectedly succeeded")
		}
		state, err = DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaLegacy {
			return fmt.Errorf("unconfirmed cutover changed state=%s err=%v", state, err)
		}
		return cutoverLegacyOutboxTx(tx, true)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			return err
		}
		state, err := DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaAbsent {
			return fmt.Errorf("post-cutover state=%s err=%v", state, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
CREATE TABLE outbox_events (
  id UUID PRIMARY KEY,
  topic TEXT NOT NULL,
  partition_key TEXT NOT NULL,
  event_type TEXT NOT NULL,
  schema_version INTEGER NOT NULL,
  aggregate_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  message JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
)`).Error; err != nil {
			return err
		}
		state, err := DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaFinal {
			return fmt.Errorf("final state=%s err=%v", state, err)
		}
		if err := cutoverLegacyOutboxTx(tx, false); err != nil {
			return fmt.Errorf("unconfirmed final cutover should be idempotent: %w", err)
		}
		if err := cutoverLegacyOutboxTx(tx, true); err != nil {
			return fmt.Errorf("confirmed final cutover should be idempotent: %w", err)
		}
		state, err = DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaFinal {
			return fmt.Errorf("post-final-idempotency state=%s err=%v", state, err)
		}
		return tx.Exec("DROP TABLE outbox_events").Error
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
CREATE TABLE outbox_events (
  id UUID PRIMARY KEY,
  payload JSONB,
  published_at TIMESTAMPTZ,
  publish_attempts INTEGER,
  topic TEXT,
  partition_key TEXT,
  event_type TEXT,
  schema_version INTEGER,
  aggregate_type TEXT,
  aggregate_id TEXT,
  message JSONB,
  occurred_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ
)`).Error; err != nil {
			return err
		}
		state, err := DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaMixed {
			return fmt.Errorf("mixed state=%s err=%v", state, err)
		}
		if err := cutoverLegacyOutboxTx(tx, true); err == nil {
			return fmt.Errorf("mixed cutover unexpectedly succeeded")
		}
		state, err = DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaMixed {
			return fmt.Errorf("mixed cutover changed state=%s err=%v", state, err)
		}
		return tx.Exec("DROP TABLE outbox_events").Error
	}); err != nil {
		t.Fatal(err)
	}
}
