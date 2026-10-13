package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenMaintenanceDatabaseRequiresConfig(t *testing.T) {
	if db, err := OpenMaintenanceDatabase(nil); db != nil || err == nil {
		t.Fatalf("OpenMaintenanceDatabase(nil)=(%v, %v), want error", db, err)
	}
}

func TestKafkaDLQDatabaseDSNUsesDedicatedEnvironmentOnly(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://application/full-access")
	t.Setenv("KAFKA_DLQ_DATABASE_DSN", " ")
	if got := KafkaDLQDatabaseDSN(); got != "" {
		t.Fatalf("KafkaDLQDatabaseDSN()=%q, want empty", got)
	}
	if db, err := OpenKafkaDLQDatabase(&Config{}); db != nil || err == nil || !strings.Contains(err.Error(), "KAFKA_DLQ_DATABASE_DSN") {
		t.Fatalf("OpenKafkaDLQDatabase without dedicated DSN=(%v,%v)", db, err)
	}
	t.Setenv("KAFKA_DLQ_DATABASE_DSN", " postgres://audit/minimal ")
	if got := KafkaDLQDatabaseDSN(); got != "postgres://audit/minimal" {
		t.Fatalf("KafkaDLQDatabaseDSN()=%q", got)
	}
}

func TestRequestAndDatabaseTimeoutDefaults(t *testing.T) {
	for _, key := range []string{
		"API_REQUEST_TIMEOUT",
		"API_UPLOAD_REQUEST_TIMEOUT",
		"POST_SEARCH_TIMEOUT",
		"DB_STATEMENT_TIMEOUT",
		"DB_LOCK_TIMEOUT",
		"API_DB_STATEMENT_TIMEOUT",
		"API_DB_LOCK_TIMEOUT",
		"WORKER_DB_STATEMENT_TIMEOUT",
		"WORKER_DB_LOCK_TIMEOUT",
		"MAINTENANCE_DB_STATEMENT_TIMEOUT",
		"MAINTENANCE_DB_LOCK_TIMEOUT",
	} {
		t.Setenv(key, "")
	}

	if got := APIRequestTimeout(); got != 12*time.Second {
		t.Errorf("APIRequestTimeout()=%s, want 12s", got)
	}
	if got := APIUploadRequestTimeout(); got != 55*time.Second {
		t.Errorf("APIUploadRequestTimeout()=%s, want 55s", got)
	}
	if got := PostSearchTimeout(); got != 2*time.Second {
		t.Errorf("PostSearchTimeout()=%s, want 2s", got)
	}
	if got := DBStatementTimeout(); got != 8*time.Second {
		t.Errorf("DBStatementTimeout()=%s, want 8s", got)
	}
	if got := DBLockTimeout(); got != 2*time.Second {
		t.Errorf("DBLockTimeout()=%s, want 2s", got)
	}
	api, err := APIDatabaseTimeoutProfile()
	if err != nil || api != (DatabaseTimeoutProfile{StatementTimeout: 8 * time.Second, LockTimeout: 2 * time.Second}) {
		t.Errorf("APIDatabaseTimeoutProfile()=(%+v, %v), want 8s/2s", api, err)
	}
	worker, err := WorkerDatabaseTimeoutProfile()
	if err != nil || worker != (DatabaseTimeoutProfile{StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}) {
		t.Errorf("WorkerDatabaseTimeoutProfile()=(%+v, %v), want 30s/5s", worker, err)
	}
	maintenance, err := MaintenanceDatabaseTimeoutProfile()
	if err != nil || maintenance != (DatabaseTimeoutProfile{StatementTimeout: 0, LockTimeout: 10 * time.Second}) {
		t.Errorf("MaintenanceDatabaseTimeoutProfile()=(%+v, %v), want disabled/10s", maintenance, err)
	}
}

func TestRequestAndDatabaseTimeoutOverrides(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "15s")
	t.Setenv("API_UPLOAD_REQUEST_TIMEOUT", "60s")
	t.Setenv("POST_SEARCH_TIMEOUT", "3s")
	t.Setenv("DB_STATEMENT_TIMEOUT", "7s")
	t.Setenv("DB_LOCK_TIMEOUT", "1500ms")
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "")
	t.Setenv("API_DB_LOCK_TIMEOUT", "")

	if got := APIRequestTimeout(); got != 15*time.Second {
		t.Errorf("APIRequestTimeout()=%s, want 15s", got)
	}
	if got := APIUploadRequestTimeout(); got != 60*time.Second {
		t.Errorf("APIUploadRequestTimeout()=%s, want 60s", got)
	}
	if got := PostSearchTimeout(); got != 3*time.Second {
		t.Errorf("PostSearchTimeout()=%s, want 3s", got)
	}
	if got := DBStatementTimeout(); got != 7*time.Second {
		t.Errorf("DBStatementTimeout()=%s, want 7s", got)
	}
	if got := DBLockTimeout(); got != 1500*time.Millisecond {
		t.Errorf("DBLockTimeout()=%s, want 1.5s", got)
	}
	api, err := APIDatabaseTimeoutProfile()
	if err != nil || api.StatementTimeout != 7*time.Second || api.LockTimeout != 1500*time.Millisecond {
		t.Errorf("API profile did not honor legacy env fallback: profile=%+v err=%v", api, err)
	}
}

func TestDatabaseTimeoutProfilesOverrideIndependently(t *testing.T) {
	t.Setenv("DB_STATEMENT_TIMEOUT", "9s")
	t.Setenv("DB_LOCK_TIMEOUT", "3s")
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "8s")
	t.Setenv("API_DB_LOCK_TIMEOUT", "2s")
	t.Setenv("WORKER_DB_STATEMENT_TIMEOUT", "30s")
	t.Setenv("WORKER_DB_LOCK_TIMEOUT", "5s")
	t.Setenv("MAINTENANCE_DB_STATEMENT_TIMEOUT", "0")
	t.Setenv("MAINTENANCE_DB_LOCK_TIMEOUT", "10s")

	api, err := APIDatabaseTimeoutProfile()
	if err != nil || api != (DatabaseTimeoutProfile{StatementTimeout: 8 * time.Second, LockTimeout: 2 * time.Second}) {
		t.Fatalf("API profile=(%+v, %v), want 8s/2s", api, err)
	}
	worker, err := WorkerDatabaseTimeoutProfile()
	if err != nil || worker != (DatabaseTimeoutProfile{StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}) {
		t.Fatalf("worker profile=(%+v, %v), want 30s/5s", worker, err)
	}
	maintenance, err := MaintenanceDatabaseTimeoutProfile()
	if err != nil || maintenance != (DatabaseTimeoutProfile{StatementTimeout: 0, LockTimeout: 10 * time.Second}) {
		t.Fatalf("maintenance profile=(%+v, %v), want 0s/10s", maintenance, err)
	}

	t.Setenv("API_DB_STATEMENT_TIMEOUT", "")
	t.Setenv("API_DB_LOCK_TIMEOUT", "")
	api, err = APIDatabaseTimeoutProfile()
	if err != nil || api != (DatabaseTimeoutProfile{StatementTimeout: 9 * time.Second, LockTimeout: 3 * time.Second}) {
		t.Fatalf("API profile did not retain legacy env fallback: profile=%+v err=%v", api, err)
	}
}

func TestDatabaseTimeoutProfilesRejectInvalidValues(t *testing.T) {
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "-1s")
	if _, err := APIDatabaseTimeoutProfile(); err == nil {
		t.Fatal("negative API statement timeout should be rejected")
	}
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "0")
	if _, err := APIDatabaseTimeoutProfile(); err == nil {
		t.Fatal("zero API statement timeout should be rejected")
	}
	t.Setenv("API_DB_STATEMENT_TIMEOUT", "")
	t.Setenv("MAINTENANCE_DB_STATEMENT_TIMEOUT", "-1s")
	if _, err := MaintenanceDatabaseTimeoutProfile(); err == nil {
		t.Fatal("negative maintenance statement timeout should be rejected")
	}
}

func TestPostgresConnConfigAppliesTimeoutsToDSNForms(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{
			name: "URL DSN preserves query parameters",
			dsn:  "postgres://service:secret@localhost:5432/exchange?sslmode=disable&application_name=timeout_test",
		},
		{
			name: "keyword value DSN preserves existing parameters",
			dsn:  "host=localhost port=5432 user=service password='secret value' dbname=exchange sslmode=disable application_name='timeout test'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connConfig, err := postgresConnConfig(test.dsn, DatabaseTimeoutProfile{StatementTimeout: 8 * time.Second, LockTimeout: 2 * time.Second})
			if err != nil {
				t.Fatalf("postgresConnConfig() error = %v", err)
			}
			if got := connConfig.RuntimeParams["statement_timeout"]; got != "8000" {
				t.Errorf("statement_timeout=%q, want 8000 milliseconds", got)
			}
			if got := connConfig.RuntimeParams["lock_timeout"]; got != "2000" {
				t.Errorf("lock_timeout=%q, want 2000 milliseconds", got)
			}
			if got := connConfig.RuntimeParams["application_name"]; got == "" {
				t.Error("application_name was not preserved")
			}
		})
	}
}

func TestPostgresConnConfigSerializesMaintenanceStatementTimeoutZero(t *testing.T) {
	connConfig, err := postgresConnConfig(
		"postgres://service:secret@localhost:5432/exchange?sslmode=disable",
		DatabaseTimeoutProfile{StatementTimeout: 0, LockTimeout: 10 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := connConfig.RuntimeParams["statement_timeout"]; got != "0" {
		t.Fatalf("maintenance statement_timeout=%q, want disabled value 0", got)
	}
	if got := connConfig.RuntimeParams["lock_timeout"]; got != "10000" {
		t.Fatalf("maintenance lock_timeout=%q, want 10000ms", got)
	}
}

func TestOpenDatabaseRejectsInvalidProfileAndPoolOptions(t *testing.T) {
	options := DatabaseOptions{
		TimeoutProfile:  DatabaseTimeoutProfile{StatementTimeout: -time.Second, LockTimeout: time.Second},
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
	}
	if _, err := openDatabase("", options); err == nil {
		t.Fatal("negative statement timeout should be rejected before opening a connection")
	}
	options.TimeoutProfile.StatementTimeout = time.Second
	options.MaxIdleConns = 2
	if _, err := openDatabase("", options); err == nil {
		t.Fatal("max idle connections above max open should be rejected")
	}
}

func TestPostgresTimeoutMillisRoundsUpAndPreservesZero(t *testing.T) {
	for _, testCase := range []struct {
		input time.Duration
		want  string
	}{
		{input: 250 * time.Millisecond, want: "250"},
		{input: 1500 * time.Microsecond, want: "2"},
		{input: time.Microsecond, want: "1"},
		{input: 0, want: "0"},
	} {
		got, err := postgresTimeoutMillis(testCase.input)
		if err != nil {
			t.Fatalf("postgresTimeoutMillis(%s) error=%v", testCase.input, err)
		}
		if got != testCase.want {
			t.Errorf("postgresTimeoutMillis(%s)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
	if _, err := postgresTimeoutMillis(-time.Second); err == nil {
		t.Fatal("negative timeout should be rejected")
	}
}

func TestRuntimeDatabasePoolOptionsUseIndependentDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{"API_DB_MAX_OPEN_CONNS", "WORKER_DB_MAX_OPEN_CONNS", "MAINTENANCE_DB_MAX_OPEN_CONNS"} {
		unsetDatabasePoolEnv(t, key)
	}
	cfg := &Config{}
	roles := []struct {
		name     string
		key      string
		fallback int
		want     DatabasePoolOptions
	}{
		{name: "api", key: "API_DB_MAX_OPEN_CONNS", fallback: apiDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 20, MaxIdleConns: 2}},
		{name: "worker", key: "WORKER_DB_MAX_OPEN_CONNS", fallback: workerDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 10, MaxIdleConns: 2}},
		{name: "maintenance", key: "MAINTENANCE_DB_MAX_OPEN_CONNS", fallback: maintenanceDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 3, MaxIdleConns: 2}},
	}
	for _, role := range roles {
		t.Run(role.name+" default", func(t *testing.T) {
			got, err := runtimeDatabasePoolOptions(cfg, role.key, role.fallback)
			if err != nil || got != role.want {
				t.Fatalf("pool options=(%+v, %v), want %+v", got, err, role.want)
			}
		})
	}

	t.Setenv("API_DB_MAX_OPEN_CONNS", "1")
	t.Setenv("WORKER_DB_MAX_OPEN_CONNS", "17")
	t.Setenv("MAINTENANCE_DB_MAX_OPEN_CONNS", "4")
	for _, test := range []struct {
		role     string
		key      string
		fallback int
		want     DatabasePoolOptions
	}{
		{role: "api", key: "API_DB_MAX_OPEN_CONNS", fallback: apiDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 1, MaxIdleConns: 1}},
		{role: "worker", key: "WORKER_DB_MAX_OPEN_CONNS", fallback: workerDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 17, MaxIdleConns: 2}},
		{role: "maintenance", key: "MAINTENANCE_DB_MAX_OPEN_CONNS", fallback: maintenanceDatabaseMaxOpenConnsDefault, want: DatabasePoolOptions{MaxOpenConns: 4, MaxIdleConns: 2}},
	} {
		t.Run(test.role+" override", func(t *testing.T) {
			got, err := runtimeDatabasePoolOptions(cfg, test.key, test.fallback)
			if err != nil || got != test.want {
				t.Fatalf("pool options=(%+v, %v), want %+v", got, err, test.want)
			}
		})
	}

	if got, err := runtimeDatabasePoolOptions(cfg, "MISSING_DB_MAX_OPEN_CONNS", 9); err != nil || got != (DatabasePoolOptions{MaxOpenConns: 9, MaxIdleConns: 2}) {
		t.Fatalf("unset role option=(%+v, %v), want fallback 9/2", got, err)
	}
}

func TestRuntimeDatabasePoolOptionsRejectInvalidExplicitValues(t *testing.T) {
	for _, key := range []string{"API_DB_MAX_OPEN_CONNS", "WORKER_DB_MAX_OPEN_CONNS", "MAINTENANCE_DB_MAX_OPEN_CONNS"} {
		for _, value := range []string{"", "0", "-1", "1.5", "many"} {
			t.Run(key+"="+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := runtimeDatabasePoolOptions(&Config{}, key, 5); err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("invalid explicit pool value %q returned error %v", value, err)
				}
			})
		}
	}
}

func unsetDatabasePoolEnv(t *testing.T, key string) {
	t.Helper()
	value, hadValue := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if hadValue {
			err = os.Setenv(key, value)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Errorf("restore %s: %v", key, err)
		}
	})
}
