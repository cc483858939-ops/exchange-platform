package config

import (
	"Go.exchange/global"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DatabaseTimeoutProfile struct {
	StatementTimeout time.Duration
	LockTimeout      time.Duration
}

type DatabaseOptions struct {
	TimeoutProfile  DatabaseTimeoutProfile
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type DatabasePoolOptions struct {
	MaxOpenConns int
	MaxIdleConns int
}

func OpenAPIDatabase(cfg *Config) (*gorm.DB, error) {
	pool, err := runtimeDatabasePoolOptions(cfg, "API_DB_MAX_OPEN_CONNS")
	if err != nil {
		return nil, err
	}
	db, err := openAPIDatabase(databaseDSN(cfg), pool)
	if err != nil {
		return nil, fmt.Errorf("open API database: %w", err)
	}
	logDatabaseProfile("api", db, pool)
	return db, nil
}

func OpenWorkerDatabase(cfg *Config) (*gorm.DB, error) {
	pool, err := runtimeDatabasePoolOptions(cfg, "WORKER_DB_MAX_OPEN_CONNS")
	if err != nil {
		return nil, err
	}
	db, err := openWorkerDatabase(databaseDSN(cfg), pool)
	if err != nil {
		return nil, fmt.Errorf("open worker database: %w", err)
	}
	logDatabaseProfile("worker", db, pool)
	return db, nil
}

func OpenMaintenanceDatabase(cfg *Config) (*gorm.DB, error) {
	if cfg == nil {
		return nil, errors.New("maintenance database configuration is nil")
	}
	return OpenMaintenanceDatabaseWithDSN(cfg, databaseDSN(cfg))
}

func OpenMaintenanceDatabaseWithDSN(cfg *Config, dsn string) (*gorm.DB, error) {
	if cfg == nil {
		return nil, errors.New("maintenance database configuration is nil")
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("maintenance database DSN is not configured")
	}
	pool := DatabasePoolOptions{
		MaxOpenConns: cfg.Database.MaxOpenConns,
		MaxIdleConns: cfg.Database.MaxIdleconns,
	}
	if override, set, err := databasePoolSizeOverride("MAINTENANCE_DB_MAX_OPEN_CONNS"); err != nil {
		return nil, fmt.Errorf("configure maintenance database pool: %w", err)
	} else if set {
		if override > pool.MaxOpenConns {
			return nil, fmt.Errorf("MAINTENANCE_DB_MAX_OPEN_CONNS (%d) exceeds total database pool budget (%d)", override, pool.MaxOpenConns)
		}
		pool.MaxOpenConns = override
		if pool.MaxIdleConns > override {
			pool.MaxIdleConns = override
		}
	}
	db, err := openMaintenanceDatabase(dsn, pool)
	if err != nil {
		return nil, fmt.Errorf("open maintenance database: %w", err)
	}
	logDatabaseProfile("maintenance", db, pool)
	return db, nil
}

func InitWorkerDatabaseConfig() {
	cfg, err := Load()
	if err != nil {
		log.Fatalf("failed to load application configuration: %v", err)
	}
	db, err := OpenWorkerDatabase(cfg)
	if err != nil {
		log.Fatalf("failed to initialize worker database: %v", err)
	}
	global.WorkerDb = db
}

func InitMaintenanceDatabaseConfig() {
	cfg, err := Load()
	if err != nil {
		log.Fatalf("failed to load application configuration: %v", err)
	}
	db, err := OpenMaintenanceDatabase(cfg)
	if err != nil {
		log.Fatalf("failed to initialize maintenance database: %v", err)
	}
	global.MaintenanceDb = db
}

func CloseDatabasePools() {
	handles := []*gorm.DB{global.APIDb, global.WorkerDb, global.MaintenanceDb}
	for _, db := range handles {
		_ = CloseDatabase(db)
	}
	global.Db = nil
	global.APIDb = nil
	global.WorkerDb = nil
	global.MaintenanceDb = nil
}

func CloseDatabase(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func openDatabase(dsn string, options DatabaseOptions) (*gorm.DB, error) {
	if err := validateDatabaseOptions(options); err != nil {
		return nil, err
	}
	connConfig, err := postgresConnConfig(dsn, options.TimeoutProfile)
	if err != nil {
		return nil, err
	}

	sqlDB := stdlib.OpenDB(*connConfig)
	sqlDB.SetMaxIdleConns(options.MaxIdleConns)
	sqlDB.SetMaxOpenConns(options.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(options.ConnMaxLifetime)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func openAPIDatabase(dsn string, pool DatabasePoolOptions) (*gorm.DB, error) {
	profile, err := APIDatabaseTimeoutProfile()
	if err != nil {
		return nil, err
	}
	return openDatabase(dsn, databaseOptions(profile, pool))
}

func openWorkerDatabase(dsn string, pool DatabasePoolOptions) (*gorm.DB, error) {
	profile, err := WorkerDatabaseTimeoutProfile()
	if err != nil {
		return nil, err
	}
	return openDatabase(dsn, databaseOptions(profile, pool))
}

func openMaintenanceDatabase(dsn string, pool DatabasePoolOptions) (*gorm.DB, error) {
	profile, err := MaintenanceDatabaseTimeoutProfile()
	if err != nil {
		return nil, err
	}
	return openDatabase(dsn, databaseOptions(profile, pool))
}

func databaseOptions(profile DatabaseTimeoutProfile, pool DatabasePoolOptions) DatabaseOptions {
	return DatabaseOptions{
		TimeoutProfile:  profile,
		MaxOpenConns:    pool.MaxOpenConns,
		MaxIdleConns:    pool.MaxIdleConns,
		ConnMaxLifetime: time.Hour,
	}
}

func validateDatabaseOptions(options DatabaseOptions) error {
	if options.TimeoutProfile.StatementTimeout < 0 {
		return errors.New("database statement timeout must not be negative")
	}
	if options.TimeoutProfile.LockTimeout < 0 {
		return errors.New("database lock timeout must not be negative")
	}
	if options.MaxOpenConns <= 0 {
		return errors.New("database max open connections must be positive")
	}
	if options.MaxIdleConns < 0 || options.MaxIdleConns > options.MaxOpenConns {
		return errors.New("database max idle connections must be between zero and max open connections")
	}
	if options.ConnMaxLifetime <= 0 {
		return errors.New("database connection max lifetime must be positive")
	}
	return nil
}

func postgresConnConfig(dsn string, profile DatabaseTimeoutProfile) (*pgx.ConnConfig, error) {
	if profile.StatementTimeout < 0 || profile.LockTimeout < 0 {
		return nil, errors.New("PostgreSQL timeout profile values must not be negative")
	}
	statementTimeout, err := postgresTimeoutMillis(profile.StatementTimeout)
	if err != nil {
		return nil, fmt.Errorf("serialize PostgreSQL statement timeout: %w", err)
	}
	lockTimeout, err := postgresTimeoutMillis(profile.LockTimeout)
	if err != nil {
		return nil, fmt.Errorf("serialize PostgreSQL lock timeout: %w", err)
	}
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL connection config: %w", err)
	}
	if connConfig.RuntimeParams == nil {
		connConfig.RuntimeParams = make(map[string]string)
	}
	connConfig.RuntimeParams["statement_timeout"] = statementTimeout
	connConfig.RuntimeParams["lock_timeout"] = lockTimeout
	return connConfig, nil
}

func postgresTimeoutMillis(timeout time.Duration) (string, error) {
	if timeout < 0 {
		return "", errors.New("timeout must not be negative")
	}
	if timeout == 0 {
		return "0", nil
	}
	millis := timeout / time.Millisecond
	if timeout%time.Millisecond != 0 {
		millis++
	}
	if millis < 1 {
		millis = 1
	}
	return strconv.FormatInt(int64(millis), 10), nil
}

func runtimeDatabasePoolOptions(cfg *Config, overrideKey string) (DatabasePoolOptions, error) {
	if cfg == nil {
		return DatabasePoolOptions{}, errors.New("application configuration is not initialized")
	}
	totalOpen := cfg.Database.MaxOpenConns
	totalIdle := cfg.Database.MaxIdleconns
	if totalOpen <= 0 {
		return DatabasePoolOptions{}, errors.New("database max open connections must be positive")
	}
	if totalIdle < 0 {
		return DatabasePoolOptions{}, errors.New("database max idle connections must not be negative")
	}
	if totalIdle > totalOpen {
		totalIdle = totalOpen
	}
	maxOpen, err := requestedPoolSize(overrideKey, totalOpen, totalOpen)
	if err != nil {
		return DatabasePoolOptions{}, err
	}
	return DatabasePoolOptions{MaxOpenConns: maxOpen, MaxIdleConns: min(totalIdle, maxOpen)}, nil
}

func databaseDSN(cfg *Config) string {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN")); dsn != "" {
		return dsn
	}
	if cfg == nil {
		return ""
	}
	return cfg.Database.Dsn
}

func requestedPoolSize(key string, fallback, totalBudget int) (int, error) {
	value, set, err := databasePoolSizeOverride(key)
	if err != nil {
		return 0, err
	}
	if !set {
		return fallback, nil
	}
	if value > totalBudget {
		return 0, fmt.Errorf("%s (%d) exceeds total database pool budget (%d)", key, value, totalBudget)
	}
	return value, nil
}

func databasePoolSizeOverride(key string) (int, bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, true, nil
}

func logDatabaseProfile(role string, db *gorm.DB, pool DatabasePoolOptions) {
	if db == nil {
		return
	}
	var profile DatabaseTimeoutProfile
	switch role {
	case "api":
		profile, _ = APIDatabaseTimeoutProfile()
	case "worker":
		profile, _ = WorkerDatabaseTimeoutProfile()
	case "maintenance":
		profile, _ = MaintenanceDatabaseTimeoutProfile()
	}
	log.Printf("database pool initialized role=%s statement_timeout=%s lock_timeout=%s max_open=%d max_idle=%d",
		role, profile.StatementTimeout, profile.LockTimeout, pool.MaxOpenConns, pool.MaxIdleConns)
}

// Reference: https://github.com/go-gorm/postgres
