package metrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type poolStatsTestConnector struct{}

func (poolStatsTestConnector) Connect(context.Context) (driver.Conn, error) {
	return poolStatsTestConn{}, nil
}

func (poolStatsTestConnector) Driver() driver.Driver { return poolStatsTestDriver{} }

type poolStatsTestDriver struct{}

func (poolStatsTestDriver) Open(string) (driver.Conn, error) { return poolStatsTestConn{}, nil }

type poolStatsTestConn struct{}

func (poolStatsTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("queries are not supported")
}
func (poolStatsTestConn) Close() error { return nil }
func (poolStatsTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func TestDatabasePoolStatsCollectorExposesSQLDBStats(t *testing.T) {
	apiDB := sql.OpenDB(poolStatsTestConnector{})
	apiDB.SetMaxOpenConns(2)
	defer apiDB.Close()

	first, err := apiDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := apiDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if conn, err := apiDB.Conn(waitCtx); err == nil {
		_ = conn.Close()
		t.Fatal("third connection unexpectedly acquired while both pool connections were held")
	}

	workerDB := sql.OpenDB(poolStatsTestConnector{})
	workerDB.SetMaxOpenConns(4)
	defer workerDB.Close()

	if err := RegisterDatabasePool(databasePoolRoleAPI, apiDB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { databasePoolCollector.unregister(databasePoolRoleAPI, apiDB) })
	if err := RegisterDatabasePool(databasePoolRoleWorker, workerDB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { databasePoolCollector.unregister(databasePoolRoleWorker, workerDB) })

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("metrics status=%d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`go_exchange_database_pool_max_open_connections{role="api"} 2`,
		`go_exchange_database_pool_open_connections{role="api"} 2`,
		`go_exchange_database_pool_in_use_connections{role="api"} 2`,
		`go_exchange_database_pool_idle_connections{role="api"} 0`,
		`go_exchange_database_pool_wait_count_total{role="api"} 1`,
		`go_exchange_database_pool_wait_duration_seconds_total{role="api"} `,
		`go_exchange_database_pool_max_open_connections{role="worker"} 4`,
		`go_exchange_database_pool_open_connections{role="worker"} 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics response missing %q", expected)
		}
	}
}

func TestRegisterDatabasePoolRejectsUnsupportedRoleAndNilPool(t *testing.T) {
	if err := RegisterDatabasePool("maintenance", nil); err == nil {
		t.Fatal("maintenance pool should not register without a long-lived metrics endpoint")
	}
	if err := RegisterDatabasePool(databasePoolRoleAPI, nil); err == nil {
		t.Fatal("nil pool should be rejected")
	}
}
