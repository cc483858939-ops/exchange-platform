package testdb

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
)

func TestOwnedPostgreSQLPoolsCloseAfterEachTestIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	for i := 0; i < 40; i++ {
		var pool *sql.DB
		t.Run(fmt.Sprintf("pool_%d", i), func(t *testing.T) {
			db, err := Open(t, dsn)
			if err != nil {
				t.Fatal(err)
			}
			pool, err = db.DB()
			if err != nil {
				t.Fatal(err)
			}
			if err := pool.PingContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if pool.Stats().MaxOpenConnections != 10 {
				t.Fatal("missing test pool limit")
			}
		})
		if pool == nil {
			t.Fatal("pool was not opened")
		}
		if err := pool.PingContext(t.Context()); err == nil {
			t.Fatal("test-owned pool remains usable after cleanup")
		}
		if pool.Stats().OpenConnections != 0 {
			t.Fatal("test-owned connections remain open after cleanup")
		}
	}
}
