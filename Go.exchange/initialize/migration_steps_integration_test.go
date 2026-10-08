package initialize

import (
	"Go.exchange/internal/testdb"
	"Go.exchange/models"
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"strings"
	"testing"
	"time"
)

type migrationDDLObserver struct {
	logger.Interface
	statements []string
}

func (o *migrationDDLObserver) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "DROP TRIGGER") {
		o.statements = append(o.statements, sql)
	}
	o.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

func TestMigrationStepsSkipRepeatedDDLAndRepairCatalogDriftIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	observer := &migrationDDLObserver{Interface: logger.Default.LogMode(logger.Silent)}
	tx := db.Session(&gorm.Session{Logger: observer}).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("migration_steps_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema + ", public").Error; err != nil {
		t.Fatal(err)
	}
	firstStart := time.Now()
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	firstElapsed, firstDDL := time.Since(firstStart), len(observer.statements)
	var steps int64
	if err := tx.Model(&models.MigrationStep{}).Count(&steps).Error; err != nil || steps != 23 {
		t.Fatalf("steps=%d err=%v", steps, err)
	}
	observer.statements = nil
	repeatStart := time.Now()
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	repeatElapsed := time.Since(repeatStart)
	t.Logf("initial: %s %d ALTER/drop-trigger statements; repeat: %s %d", firstElapsed, firstDDL, repeatElapsed, len(observer.statements))
	if len(observer.statements) != 0 {
		t.Fatalf("unchanged catalog still ran DDL: %v", observer.statements)
	}
	for _, change := range []string{
		"DROP INDEX idx_posts_search_public_created",
		"ALTER TABLE posts DROP CONSTRAINT chk_posts_like_count_nonnegative",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_like_count_nonnegative CHECK (like_count >= -1)",
		"CREATE OR REPLACE FUNCTION reject_outbox_event_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$",
	} {
		if err := tx.Exec(change).Error; err != nil {
			t.Fatal(err)
		}
	}
	observer.statements = nil
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if len(observer.statements) == 0 {
		t.Fatal("catalog drift did not rerun explicit DDL")
	}
	var definition string
	if err := tx.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'posts'::regclass AND conname='chk_posts_like_count_nonnegative'").Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(definition, "-1") {
		t.Fatalf("same-name wrong constraint not repaired: %s", definition)
	}
	if !tx.Migrator().HasIndex(&models.Post{}, "idx_posts_search_public_created") {
		t.Fatal("missing index not repaired")
	}
	if err := tx.Raw("SELECT pg_get_functiondef('reject_outbox_event_update()'::regprocedure)").Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "RAISE EXCEPTION") {
		t.Fatal("same-name trigger function not repaired")
	}
	if err := tx.Model(&models.MigrationStep{}).Where("id = ?", "apply post schema constraints").Update("sql_checksum", "changed").Error; err != nil {
		t.Fatal(err)
	}
	observer.statements = nil
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if len(observer.statements) == 0 {
		t.Fatal("SQL checksum change skipped migration")
	}
	observer.statements = nil
	if err := RunMigrationsWithDB(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if len(observer.statements) != 0 {
		t.Fatal("repaired migration failed to refresh checkpoint")
	}
	var fingerprintWidth int64
	if err := tx.Raw("SELECT character_maximum_length FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='posts' AND column_name='client_publish_fingerprint'").Scan(&fingerprintWidth).Error; err != nil || fingerprintWidth != 64 {
		t.Fatalf("CHAR fingerprint width=%d err=%v", fingerprintWidth, err)
	}
	var before models.MigrationStep
	if err := tx.Where("id = ?", "apply post schema constraints").First(&before).Error; err != nil {
		t.Fatal(err)
	}
	err = tx.Transaction(func(nested *gorm.DB) error {
		tracked, err := beginMigrationSteps(nested)
		if err != nil {
			return err
		}
		if err := applyMigrationStatements(tracked, before.ID, []string{
			"ALTER TABLE posts ADD COLUMN failed_step_probe integer",
			"ALTER TABLE posts ADD CONSTRAINT failed_step_check CHECK (missing_step_column > 0)",
		}); err != nil {
			return err
		}
		return finishMigrationSteps(tracked)
	})
	if err == nil {
		t.Fatal("invalid migration unexpectedly succeeded")
	}
	var after models.MigrationStep
	if err := tx.Where("id = ?", before.ID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.SQLChecksum != before.SQLChecksum || !after.AppliedAt.Equal(before.AppliedAt) || tx.Migrator().HasColumn(&models.Post{}, "failed_step_probe") {
		t.Fatal("failed migration advanced its checkpoint or left partial DDL")
	}
}
