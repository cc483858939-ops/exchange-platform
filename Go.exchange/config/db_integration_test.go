package config

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
)

func TestPostgresRuntimeTimeoutsAndCancellation(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL timeout integration test")
	}
	t.Setenv("DB_STATEMENT_TIMEOUT", "250ms")
	t.Setenv("DB_LOCK_TIMEOUT", "100ms")
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "")
	t.Setenv("API_DB_LOCK_TIMEOUT", "")

	db, err := openAPIDatabase(dsn, DatabasePoolOptions{MaxOpenConns: 4, MaxIdleConns: 4})
	if err != nil {
		t.Fatalf("openAPIDatabase() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
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

func TestPostgresDatabaseTimeoutProfilesAreIsolated(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL timeout profile isolation test")
	}
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "250ms")
	t.Setenv("API_DB_LOCK_TIMEOUT", "100ms")
	t.Setenv("WORKER_DB_STATEMENT_TIMEOUT", "500ms")
	t.Setenv("WORKER_DB_LOCK_TIMEOUT", "500ms")
	t.Setenv("MAINTENANCE_DB_STATEMENT_TIMEOUT", "0")
	t.Setenv("MAINTENANCE_DB_LOCK_TIMEOUT", "1s")

	pool := DatabasePoolOptions{MaxOpenConns: 4, MaxIdleConns: 2}
	apiDB, err := openAPIDatabase(dsn, pool)
	if err != nil {
		t.Fatalf("open API database: %v", err)
	}
	workerDB, err := openWorkerDatabase(dsn, pool)
	if err != nil {
		closeDatabaseHandle(apiDB)
		t.Fatalf("open worker database: %v", err)
	}
	maintenanceDB, err := openMaintenanceDatabase(dsn, pool)
	if err != nil {
		closeDatabaseHandle(apiDB)
		closeDatabaseHandle(workerDB)
		t.Fatalf("open maintenance database: %v", err)
	}
	t.Cleanup(func() {
		closeDatabaseHandle(apiDB)
		closeDatabaseHandle(workerDB)
		closeDatabaseHandle(maintenanceDB)
	})

	apiSQL, err := apiDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	workerSQL, err := workerDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	maintenanceSQL, err := maintenanceDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	assertSetting := func(name string, db *sql.DB, setting, want string) {
		t.Helper()
		var got string
		if err := db.QueryRowContext(context.Background(), "SHOW "+setting).Scan(&got); err != nil {
			t.Fatalf("SHOW %s on %s database: %v", setting, name, err)
		}
		if got != want {
			t.Errorf("%s %s=%q, want %q", name, setting, got, want)
		}
	}
	assertSetting("API", apiSQL, "statement_timeout", "250ms")
	assertSetting("API", apiSQL, "lock_timeout", "100ms")
	assertSetting("worker", workerSQL, "statement_timeout", "500ms")
	assertSetting("worker", workerSQL, "lock_timeout", "500ms")
	assertSetting("maintenance", maintenanceSQL, "statement_timeout", "0")
	assertSetting("maintenance", maintenanceSQL, "lock_timeout", "1s")

	err = apiDB.WithContext(context.Background()).Exec("SELECT pg_sleep(0.5)").Error
	if err == nil {
		t.Fatal("API query longer than its timeout unexpectedly succeeded")
	}
	assertPostgresSQLState(t, err, "57014")
	if err := workerDB.WithContext(context.Background()).Exec("SELECT pg_sleep(0.25)").Error; err != nil {
		t.Fatalf("worker query within worker timeout failed: %v", err)
	}
	err = workerDB.WithContext(context.Background()).Exec("SELECT pg_sleep(1)").Error
	if err == nil {
		t.Fatal("worker query longer than its timeout unexpectedly succeeded")
	}
	assertPostgresSQLState(t, err, "57014")

	cancelCtx, cancel := context.WithCancel(context.Background())
	queryDone := make(chan error, 1)
	go func() {
		queryDone <- workerDB.WithContext(cancelCtx).Exec("SELECT pg_sleep(5)").Error
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-queryDone:
		if err == nil {
			t.Fatal("worker query canceled by job context unexpectedly succeeded")
		}
		if !errors.Is(err, context.Canceled) {
			assertPostgresSQLState(t, err, "57014")
		}
	case <-time.After(time.Second):
		t.Fatal("worker database query did not honor job context cancellation")
	}

	lockCtx, lockCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer lockCancel()
	ownerConn, err := maintenanceSQL.Conn(lockCtx)
	if err != nil {
		t.Fatalf("acquire advisory lock owner connection: %v", err)
	}
	defer ownerConn.Close()
	lockID := time.Now().UnixNano()
	ownerTx, err := ownerConn.BeginTx(lockCtx, nil)
	if err != nil {
		t.Fatalf("begin advisory lock owner transaction: %v", err)
	}
	defer ownerTx.Rollback()
	if _, err := ownerTx.ExecContext(lockCtx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		t.Fatalf("acquire advisory lock: %v", err)
	}
	_, err = apiSQL.ExecContext(lockCtx, "SELECT pg_advisory_xact_lock($1)", lockID)
	if err == nil {
		t.Fatal("API advisory lock wait exceeded its lock timeout without an error")
	}
	assertPostgresSQLState(t, err, "55P03")

	released := make(chan error, 1)
	time.AfterFunc(250*time.Millisecond, func() { released <- ownerTx.Commit() })
	if _, err := workerSQL.ExecContext(lockCtx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		t.Fatalf("worker advisory lock wait should fit its independent 500ms budget: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatalf("release advisory lock owner: %v", err)
	}

	migrationLockID := lockID + 1
	migrationOwner, err := maintenanceSQL.Conn(lockCtx)
	if err != nil {
		t.Fatalf("acquire migration advisory lock owner connection: %v", err)
	}
	defer migrationOwner.Close()
	migrationTx, err := migrationOwner.BeginTx(lockCtx, nil)
	if err != nil {
		t.Fatalf("begin migration advisory lock owner transaction: %v", err)
	}
	defer migrationTx.Rollback()
	if _, err := migrationTx.ExecContext(lockCtx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		t.Fatalf("acquire migration advisory lock: %v", err)
	}
	time.AfterFunc(250*time.Millisecond, func() { _ = migrationTx.Commit() })
	if _, err := maintenanceSQL.ExecContext(lockCtx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		t.Fatalf("maintenance advisory lock should wait within its separate 1s budget: %v", err)
	}
}

func TestPostgresRuntimeAllInitializesIndependentAPIsAndWorkerPools(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL runtime=all pool isolation test")
	}
	t.Setenv("APP_RUNTIME_ROLE", RuntimeRoleAll)
	t.Setenv("DATABASE_DSN", dsn)
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "250ms")
	t.Setenv("API_DB_LOCK_TIMEOUT", "100ms")
	t.Setenv("WORKER_DB_STATEMENT_TIMEOUT", "500ms")
	t.Setenv("WORKER_DB_LOCK_TIMEOUT", "500ms")
	t.Setenv("API_DB_MAX_OPEN_CONNS", "")
	t.Setenv("WORKER_DB_MAX_OPEN_CONNS", "")

	previousConfig := AppConfig
	previousDB, previousAPIDB, previousWorkerDB := global.Db, global.APIDb, global.WorkerDb
	AppConfig = &Config{}
	AppConfig.Database.Dsn = dsn
	AppConfig.Database.MaxOpenConns = 4
	AppConfig.Database.MaxIdleconns = 2
	var openedAPI, openedWorker interface{ DB() (*sql.DB, error) }
	t.Cleanup(func() {
		if openedAPI != nil {
			if db, err := openedAPI.DB(); err == nil {
				_ = db.Close()
			}
		}
		if openedWorker != nil {
			if db, err := openedWorker.DB(); err == nil {
				_ = db.Close()
			}
		}
		global.Db, global.APIDb, global.WorkerDb = previousDB, previousAPIDB, previousWorkerDB
		AppConfig = previousConfig
	})

	if err := initDB(); err != nil {
		t.Fatalf("initialize runtime=all databases: %v", err)
	}
	openedAPI, openedWorker = global.APIDb, global.WorkerDb
	if global.APIDb == nil || global.WorkerDb == nil || global.APIDb == global.WorkerDb {
		t.Fatal("runtime=all must create distinct API and Worker handles")
	}
	if global.Db != global.APIDb {
		t.Fatal("global.Db must remain an alias for the API database")
	}

	apiSQL, err := global.APIDb.DB()
	if err != nil {
		t.Fatal(err)
	}
	workerSQL, err := global.WorkerDb.DB()
	if err != nil {
		t.Fatal(err)
	}
	if got := apiSQL.Stats().MaxOpenConnections; got != 3 {
		t.Errorf("API max open connections=%d, want 3", got)
	}
	if got := workerSQL.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("worker max open connections=%d, want 1", got)
	}
	if apiSQL.Stats().MaxOpenConnections+workerSQL.Stats().MaxOpenConnections > AppConfig.Database.MaxOpenConns {
		t.Fatal("runtime=all pool caps exceed configured total connection budget")
	}
	var apiStatement, workerStatement string
	if err := apiSQL.QueryRowContext(context.Background(), "SHOW statement_timeout").Scan(&apiStatement); err != nil {
		t.Fatal(err)
	}
	if err := workerSQL.QueryRowContext(context.Background(), "SHOW statement_timeout").Scan(&workerStatement); err != nil {
		t.Fatal(err)
	}
	if apiStatement != "250ms" || workerStatement != "500ms" {
		t.Fatalf("runtime=all statement profiles API=%q worker=%q", apiStatement, workerStatement)
	}
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
