package initialize

import (
	"fmt"
	"os"
	"strings"
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
	  id UUID PRIMARY KEY,
	  aggregate_type VARCHAR(64) NOT NULL,
	  aggregate_id VARCHAR(128) NOT NULL,
	  event_type VARCHAR(128) NOT NULL,
	  schema_version INTEGER NOT NULL,
	  payload JSONB NOT NULL,
	  occurred_at TIMESTAMPTZ NOT NULL,
	  published_at TIMESTAMPTZ,
	  publish_attempts INTEGER NOT NULL DEFAULT 0,
	  last_error TEXT,
	  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`).Error; err != nil {
			return err
		}
		state, err := DetectOutboxSchema(tx)
		if err != nil || state != OutboxSchemaLegacy {
			return fmt.Errorf("legacy state=%s err=%v", state, err)
		}
		if err := tx.Exec(`
INSERT INTO outbox_events
  (id, aggregate_type, aggregate_id, event_type, schema_version, payload, occurred_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'test', 'aggregate', 'test.event', 1, '{}'::jsonb, now())`).Error; err != nil {
			return err
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
		var rowCount int64
		if err := tx.Table("outbox_events").Count(&rowCount).Error; err != nil {
			return err
		}
		if rowCount != 0 {
			return fmt.Errorf("unexpected mixed fixture rows=%d", rowCount)
		}
		return tx.Exec("DROP TABLE outbox_events").Error
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxCutoverRejectsNearLegacySchemasIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("outbox_near_legacy_%d", os.Getpid())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error })

	cases := []struct {
		name   string
		remove string
		add    string
	}{
		{name: "missing aggregate_type", remove: "aggregate_type"},
		{name: "missing occurred_at", remove: "occurred_at"},
		{name: "missing last_error", remove: "last_error"},
		{name: "final message column added", add: "message JSONB"},
		{name: "unknown delivery column", add: "unexpected_delivery_state TEXT"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
					return err
				}
				definitions := []string{
					"id UUID PRIMARY KEY",
					"aggregate_type VARCHAR(64)",
					"aggregate_id VARCHAR(128)",
					"event_type VARCHAR(128)",
					"schema_version INTEGER",
					"payload JSONB",
					"occurred_at TIMESTAMPTZ",
					"published_at TIMESTAMPTZ",
					"publish_attempts INTEGER",
					"last_error TEXT",
					"created_at TIMESTAMPTZ",
					"updated_at TIMESTAMPTZ",
				}
				if testCase.remove != "" {
					filtered := definitions[:0]
					for _, definition := range definitions {
						if !strings.HasPrefix(definition, testCase.remove+" ") {
							filtered = append(filtered, definition)
						}
					}
					definitions = filtered
				}
				if testCase.add != "" {
					definitions = append(definitions, testCase.add)
				}
				if err := tx.Exec("CREATE TABLE outbox_events (" + strings.Join(definitions, ",") + ")").Error; err != nil {
					return err
				}
				if err := tx.Exec("INSERT INTO outbox_events (id) VALUES ('00000000-0000-0000-0000-000000000002')").Error; err != nil {
					return err
				}
				state, err := DetectOutboxSchema(tx)
				if err != nil {
					return err
				}
				if state != OutboxSchemaMixed {
					return fmt.Errorf("state=%s want MIXED", state)
				}
				if err := cutoverLegacyOutboxTx(tx, true); err == nil {
					return fmt.Errorf("near-legacy cutover unexpectedly succeeded")
				}
				if !tx.Migrator().HasTable("outbox_events") {
					return fmt.Errorf("mixed table was removed")
				}
				var rowCount int64
				if err := tx.Table("outbox_events").Count(&rowCount).Error; err != nil {
					return err
				}
				if rowCount != 1 {
					return fmt.Errorf("mixed fixture rows=%d want=1", rowCount)
				}
				return tx.Exec("DROP TABLE outbox_events").Error
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
