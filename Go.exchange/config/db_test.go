package config

import (
	"testing"
	"time"
)

func TestRequestAndDatabaseTimeoutDefaults(t *testing.T) {
	for _, key := range []string{
		"API_REQUEST_TIMEOUT",
		"API_UPLOAD_REQUEST_TIMEOUT",
		"DB_STATEMENT_TIMEOUT",
		"DB_LOCK_TIMEOUT",
	} {
		t.Setenv(key, "")
	}

	if got := APIRequestTimeout(); got != 12*time.Second {
		t.Errorf("APIRequestTimeout()=%s, want 12s", got)
	}
	if got := APIUploadRequestTimeout(); got != 55*time.Second {
		t.Errorf("APIUploadRequestTimeout()=%s, want 55s", got)
	}
	if got := DBStatementTimeout(); got != 8*time.Second {
		t.Errorf("DBStatementTimeout()=%s, want 8s", got)
	}
	if got := DBLockTimeout(); got != 2*time.Second {
		t.Errorf("DBLockTimeout()=%s, want 2s", got)
	}
}

func TestRequestAndDatabaseTimeoutOverrides(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "15s")
	t.Setenv("API_UPLOAD_REQUEST_TIMEOUT", "60s")
	t.Setenv("DB_STATEMENT_TIMEOUT", "7s")
	t.Setenv("DB_LOCK_TIMEOUT", "1500ms")

	if got := APIRequestTimeout(); got != 15*time.Second {
		t.Errorf("APIRequestTimeout()=%s, want 15s", got)
	}
	if got := APIUploadRequestTimeout(); got != 60*time.Second {
		t.Errorf("APIUploadRequestTimeout()=%s, want 60s", got)
	}
	if got := DBStatementTimeout(); got != 7*time.Second {
		t.Errorf("DBStatementTimeout()=%s, want 7s", got)
	}
	if got := DBLockTimeout(); got != 1500*time.Millisecond {
		t.Errorf("DBLockTimeout()=%s, want 1.5s", got)
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
			connConfig, err := postgresConnConfig(test.dsn, 8*time.Second, 2*time.Second)
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

func TestPostgresTimeoutMillisRoundsUpAndClampsPositiveSubMillisecondValues(t *testing.T) {
	for _, testCase := range []struct {
		input time.Duration
		want  string
	}{
		{input: 250 * time.Millisecond, want: "250"},
		{input: 1500 * time.Microsecond, want: "2"},
		{input: time.Microsecond, want: "1"},
	} {
		if got := postgresTimeoutMillis(testCase.input); got != testCase.want {
			t.Errorf("postgresTimeoutMillis(%s)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
}
