package initialize

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"Go.exchange/global"

	"gorm.io/gorm"
)

// OutboxSchemaState describes only the schema shapes that are safe to handle
// automatically. Anything that is not clearly absent, legacy, or final is
// mixed and must fail closed.
type OutboxSchemaState int

const (
	OutboxSchemaAbsent OutboxSchemaState = iota
	OutboxSchemaLegacy
	OutboxSchemaFinal
	OutboxSchemaMixed
)

const outboxCutoverConfirmationEnv = "OUTBOX_CUTOVER_CONFIRM_PRELAUNCH"

var finalOutboxColumns = []string{
	"id", "topic", "partition_key", "event_type", "schema_version",
	"aggregate_type", "aggregate_id", "message", "occurred_at", "created_at",
}

var legacyOutboxColumns = []string{
	"payload", "published_at", "publish_attempts", "last_error", "updated_at",
}

func (state OutboxSchemaState) String() string {
	switch state {
	case OutboxSchemaAbsent:
		return "ABSENT"
	case OutboxSchemaLegacy:
		return "LEGACY"
	case OutboxSchemaFinal:
		return "FINAL"
	case OutboxSchemaMixed:
		return "MIXED"
	default:
		return "UNKNOWN"
	}
}

// DetectOutboxSchema reads the live information_schema and deliberately does
// not use GORM model inference. This protects both the normal migration and
// the explicitly destructive cutover command from guessing about old tables.
func DetectOutboxSchema(tx *gorm.DB) (OutboxSchemaState, error) {
	if tx == nil {
		return OutboxSchemaMixed, errors.New("database transaction is nil")
	}
	var names []string
	if err := tx.Raw(`
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'outbox_events'
`).Scan(&names).Error; err != nil {
		return OutboxSchemaMixed, fmt.Errorf("inspect outbox_events columns: %w", err)
	}
	if len(names) == 0 {
		return OutboxSchemaAbsent, nil
	}
	columns := make(map[string]struct{}, len(names))
	for _, name := range names {
		columns[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	has := func(name string) bool {
		_, ok := columns[name]
		return ok
	}
	final := true
	for _, name := range finalOutboxColumns {
		if !has(name) {
			final = false
			break
		}
	}
	legacyEvidence := has("payload") && has("published_at") && has("publish_attempts") &&
		!has("message") && !has("topic") && !has("partition_key")
	hasLegacyColumn := false
	for _, name := range legacyOutboxColumns {
		if has(name) {
			hasLegacyColumn = true
			break
		}
	}
	if legacyEvidence && !final {
		return OutboxSchemaLegacy, nil
	}
	if final && !hasLegacyColumn {
		return OutboxSchemaFinal, nil
	}
	return OutboxSchemaMixed, nil
}

// prepareLegacyOutboxSchema is intentionally defensive. The regular migrate
// command never performs the destructive legacy conversion; operators must
// run outbox-cutover first and then migrate.
func prepareLegacyOutboxSchema(tx *gorm.DB) error {
	state, err := DetectOutboxSchema(tx)
	if err != nil {
		return err
	}
	switch state {
	case OutboxSchemaAbsent, OutboxSchemaFinal:
		return nil
	case OutboxSchemaLegacy:
		return fmt.Errorf("legacy outbox_events schema detected; run outbox-cutover with %s=true after confirming the pre-launch no-history assumption", outboxCutoverConfirmationEnv)
	case OutboxSchemaMixed:
		return errors.New("mixed outbox_events schema detected; refusing automatic repair")
	default:
		return fmt.Errorf("unknown outbox schema state %d", state)
	}
}

func OutboxCutoverConfirmed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(outboxCutoverConfirmationEnv)), "true")
}

// RunOutboxCutover performs the only supported destructive legacy conversion.
// It is separate from RunMigrations so a normal service startup can never
// erase an Outbox table implicitly.
func RunOutboxCutover() error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return CutoverLegacyOutbox(global.Db, OutboxCutoverConfirmed())
}

func CutoverLegacyOutbox(db *gorm.DB, confirmed bool) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return cutoverLegacyOutboxTx(tx, confirmed)
	})
}

func cutoverLegacyOutboxTx(tx *gorm.DB, confirmed bool) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationAdvisoryLockKey).Error; err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	state, err := DetectOutboxSchema(tx)
	if err != nil {
		return err
	}
	switch state {
	case OutboxSchemaAbsent, OutboxSchemaFinal:
		return nil
	case OutboxSchemaMixed:
		return errors.New("mixed outbox_events schema detected; refusing destructive cutover")
	case OutboxSchemaLegacy:
		if !confirmed {
			return fmt.Errorf("legacy outbox_events schema requires %s=true", outboxCutoverConfirmationEnv)
		}
	default:
		return fmt.Errorf("unknown outbox schema state %d", state)
	}

	for _, resource := range []struct {
		name  string
		query string
	}{
		{name: "publication", query: "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'goexchange_outbox_pub')"},
		{name: "replication slot", query: "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'goexchange_outbox_slot')"},
	} {
		var exists bool
		if err := tx.Raw(resource.query).Scan(&exists).Error; err != nil {
			return fmt.Errorf("check CDC %s: %w", resource.name, err)
		}
		if exists {
			return fmt.Errorf("legacy outbox cutover refused: CDC %s already exists", resource.name)
		}
	}

	var rowCount int64
	if err := tx.Table("outbox_events").Count(&rowCount).Error; err != nil {
		return fmt.Errorf("count legacy outbox rows: %w", err)
	}
	log.Printf("outbox-cutover: dropping legacy outbox_events with %d rows", rowCount)
	if err := tx.Exec("DROP TABLE outbox_events").Error; err != nil {
		return fmt.Errorf("drop legacy outbox_events table: %w", err)
	}
	return nil
}
