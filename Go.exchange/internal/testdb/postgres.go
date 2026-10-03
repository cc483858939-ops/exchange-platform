// Package testdb owns PostgreSQL pools used by integration tests. It is never
// used to manage application/global database lifetimes.
package testdb

import (
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open registers pool cleanup immediately, before migrations, fixture setup or
// assertions can fail. Later fixture cleanups run first (t.Cleanup is LIFO).
func Open(t testing.TB, dsn string) (*gorm.DB, error) {
	t.Helper()
	db, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if db != nil {
		pool, err := db.DB()
		if err != nil {
			return nil, err
		}
		pool.SetMaxOpenConns(10)
		pool.SetMaxIdleConns(2)
		t.Cleanup(func() {
			if err := pool.Close(); err != nil {
				t.Errorf("close test-owned PostgreSQL pool: %v", err)
			}
		})
	}
	return db, openErr
}
