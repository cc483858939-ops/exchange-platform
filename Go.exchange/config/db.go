package config

import (
	"Go.exchange/global"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func initDB() {
	dsn := DatabaseDSN()
	db, err := openDatabase(dsn)
	if err != nil {
		log.Fatalf("Failed to initialize datebase,got erro:%v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to configure database, got error: %v", err)
	}
	sqlDB.SetMaxIdleConns(AppConfig.Database.MaxIdleconns)
	sqlDB.SetMaxOpenConns(AppConfig.Database.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)
	global.Db = db
}

func openDatabase(dsn string) (*gorm.DB, error) {
	connConfig, err := postgresConnConfig(dsn, DBStatementTimeout(), DBLockTimeout())
	if err != nil {
		return nil, err
	}

	sqlDB := stdlib.OpenDB(*connConfig)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func postgresConnConfig(dsn string, statementTimeout, lockTimeout time.Duration) (*pgx.ConnConfig, error) {
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL connection config: %w", err)
	}
	if connConfig.RuntimeParams == nil {
		connConfig.RuntimeParams = make(map[string]string)
	}
	connConfig.RuntimeParams["statement_timeout"] = postgresTimeoutMillis(statementTimeout)
	connConfig.RuntimeParams["lock_timeout"] = postgresTimeoutMillis(lockTimeout)
	return connConfig, nil
}

func postgresTimeoutMillis(timeout time.Duration) string {
	millis := timeout / time.Millisecond
	if timeout%time.Millisecond != 0 {
		millis++
	}
	if millis < 1 {
		millis = 1
	}
	return strconv.FormatInt(int64(millis), 10)
}

// Reference: https://github.com/go-gorm/postgres
