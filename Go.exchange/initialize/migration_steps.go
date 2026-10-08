package initialize

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type migrationStepsContextKey struct{}
type migrationSteps struct {
	before   string
	previous map[string]models.MigrationStep
	seen     map[string]string
	changed  bool
}

// Model migration still runs. This checkpoint removes repeated explicit DDL
// only when its SQL AND the actual catalog match the last successful run.
func beginMigrationSteps(tx *gorm.DB) (*gorm.DB, error) {
	fingerprint, err := migrationCatalogFingerprint(tx)
	if err != nil {
		return nil, err
	}
	var rows []models.MigrationStep
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	tracker := &migrationSteps{before: fingerprint, previous: make(map[string]models.MigrationStep), seen: make(map[string]string)}
	for _, row := range rows {
		tracker.previous[row.ID] = row
	}
	return tx.WithContext(context.WithValue(tx.Statement.Context, migrationStepsContextKey{}, tracker)), nil
}

func applyMigrationStatements(tx *gorm.DB, id string, statements []string) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(statements, "\x00"))))
	tracker, _ := tx.Statement.Context.Value(migrationStepsContextKey{}).(*migrationSteps)
	if tracker != nil {
		tracker.seen[id] = checksum
		previous, ok := tracker.previous[id]
		if !tracker.changed && ok && previous.SQLChecksum == checksum && previous.CatalogFingerprint == tracker.before {
			return nil
		}
		// A changed earlier step may affect later steps: execute the remainder.
		tracker.changed = true
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func finishMigrationSteps(tx *gorm.DB) error {
	tracker, _ := tx.Statement.Context.Value(migrationStepsContextKey{}).(*migrationSteps)
	if tracker == nil || !tracker.changed {
		return nil
	}
	fingerprint, err := migrationCatalogFingerprint(tx)
	if err != nil {
		return err
	}
	rows := make([]models.MigrationStep, 0, len(tracker.seen))
	for id, checksum := range tracker.seen {
		rows = append(rows, models.MigrationStep{ID: id, SQLChecksum: checksum, CatalogFingerprint: fingerprint, AppliedAt: time.Now().UTC()})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&rows).Error
}

// Include definitions, types, widths, validation flags, defaults, index health,
// triggers and functions. Names alone cannot detect a same-name broken object.
func migrationCatalogFingerprint(tx *gorm.DB) (string, error) {
	var fingerprint string
	err := tx.Raw(`
WITH catalog AS (
 SELECT 'column' AS kind, c.relname || '.' || a.attname AS identity,
  jsonb_build_array(c.relkind, format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,
   pg_get_expr(d.adbin,d.adrelid))::text AS definition
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','v','m')
 UNION ALL
 SELECT 'constraint', c.relname || '.' || k.conname,
  jsonb_build_array(pg_get_constraintdef(k.oid),k.convalidated,k.condeferrable,k.condeferred)::text
 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema()
 UNION ALL
 SELECT 'index', c.relname, jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready)::text
 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema()
 UNION ALL
 SELECT 'trigger', c.relname || '.' || t.tgname, jsonb_build_array(pg_get_triggerdef(t.oid),t.tgenabled)::text
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND NOT t.tgisinternal
 UNION ALL
 SELECT 'function', p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')', pg_get_functiondef(p.oid)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() AND p.prokind IN ('f','p')
 UNION ALL
 SELECT 'extension', extname, extversion FROM pg_extension
)
SELECT md5(COALESCE(string_agg(jsonb_build_array(kind,identity,definition)::text,E'\n' ORDER BY kind,identity,definition),'')) FROM catalog
`).Scan(&fingerprint).Error
	if err != nil {
		return "", fmt.Errorf("read migration catalog fingerprint: %w", err)
	}
	return fingerprint, nil
}
