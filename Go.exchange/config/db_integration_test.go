package config

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPostgresRuntimeTimeoutsAndCancellation(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL timeout integration test")
	}
	t.Setenv("DB_STATEMENT_TIMEOUT", "250ms")
	t.Setenv("DB_LOCK_TIMEOUT", "100ms")

	db, err := openDatabase(dsn)
	if err != nil {
		t.Fatalf("openDatabase() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var settings []struct {
		Name    string `gorm:"column:name"`
		Setting int    `gorm:"column:setting"`
	}
	if err := db.Raw(`SELECT name, setting::int AS setting FROM pg_settings WHERE name IN ('statement_timeout', 'lock_timeout')`).Scan(&settings).Error; err != nil {
		t.Fatalf("read PostgreSQL timeout settings: %v", err)
	}
	gotSettings := make(map[string]int, len(settings))
	for _, setting := range settings {
		gotSettings[setting.Name] = setting.Setting
	}
	if gotSettings["statement_timeout"] != 250 {
		t.Errorf("statement_timeout=%dms, want 250ms", gotSettings["statement_timeout"])
	}
	if gotSettings["lock_timeout"] != 100 {
		t.Errorf("lock_timeout=%dms, want 100ms", gotSettings["lock_timeout"])
	}

	started := time.Now()
	err = db.WithContext(context.Background()).Exec("SELECT pg_sleep(2)").Error
	if err == nil {
		t.Fatal("statement exceeded configured statement_timeout without error")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Errorf("statement_timeout query took %s, want under 1s", elapsed)
	}
	assertPostgresSQLState(t, err, "57014")

	rollbackConn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire rollback test connection: %v", err)
	}
	defer rollbackConn.Close()
	if _, err := rollbackConn.ExecContext(context.Background(), `CREATE TEMP TABLE request_timeout_rollback (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("create transaction rollback table: %v", err)
	}
	tx, err := rollbackConn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin rollback verification transaction: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO request_timeout_rollback (id) VALUES (1)`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert rollback row: %v", err)
	}
	_, err = tx.ExecContext(context.Background(), `SELECT pg_sleep(2)`)
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("statement timeout inside transaction returned without an error")
	}
	assertPostgresSQLState(t, err, "57014")
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback timed out transaction: %v", err)
	}
	var remaining int
	if err := rollbackConn.QueryRowContext(context.Background(), `SELECT count(*) FROM request_timeout_rollback`).Scan(&remaining); err != nil {
		t.Fatalf("query after transaction rollback: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("rows after timed out transaction rollback=%d, want 0", remaining)
	}

	ctx, cancel := context.WithCancel(context.Background())
	queryDone := make(chan error, 1)
	go func() {
		queryDone <- db.WithContext(ctx).Exec("SELECT pg_sleep(2)").Error
	}()
	time.Sleep(75 * time.Millisecond)
	cancel()
	select {
	case err := <-queryDone:
		if err == nil {
			t.Fatal("canceled query returned without an error")
		}
		if !errors.Is(err, context.Canceled) {
			assertPostgresSQLState(t, err, "57014")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled query did not return promptly")
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer deadlineCancel()
	started = time.Now()
	err = db.WithContext(deadlineCtx).Exec("SELECT pg_sleep(2)").Error
	if err == nil {
		t.Fatal("query exceeding context deadline returned without an error")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Errorf("context deadline query took %s, want under 1s", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		assertPostgresSQLState(t, err, "57014")
	}

	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connA, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection A: %v", err)
	}
	defer connA.Close()
	txA, err := connA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin lock transaction A: %v", err)
	}
	defer txA.Rollback()
	lockID := time.Now().UnixNano()
	if _, err := txA.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		t.Fatalf("acquire advisory lock in transaction A: %v", err)
	}
	connB, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection B: %v", err)
	}
	defer connB.Close()
	started = time.Now()
	_, err = connB.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID)
	if err == nil {
		t.Fatal("contended advisory lock returned without an error")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Errorf("lock timeout query took %s, want under 1s", elapsed)
	}
	assertPostgresSQLState(t, err, "55P03")
}

func assertPostgresSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var stateError interface{ SQLState() string }
	if !errors.As(err, &stateError) {
		t.Fatalf("error %T (%v) does not expose SQLSTATE", err, err)
	}
	if got := stateError.SQLState(); got != want {
		t.Fatalf("SQLSTATE=%s, want %s (error: %v)", got, want, err)
	}
}
