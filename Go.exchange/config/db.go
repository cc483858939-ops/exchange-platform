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

type DatabasePoolAllocation struct {
	API    DatabasePoolOptions
	Worker DatabasePoolOptions
}

func initDB() error {
	if AppConfig == nil {
		return errors.New("application configuration is not initialized")
	}
	role := RuntimeRole()
	pools, err := runtimeDatabasePoolAllocation(role)
	if err != nil {
		return err
	}
	apiDB, workerDB, err := openRuntimeDatabaseHandles(DatabaseDSN(), role, pools)
	if err != nil {
		return err
	}

	global.APIDb = apiDB
	global.Db = apiDB
	global.WorkerDb = workerDB
	if apiDB != nil {
		logDatabaseProfile("api", apiDB, pools.API)
	}
	if workerDB != nil {
		logDatabaseProfile("worker", workerDB, pools.Worker)
	}
	return nil
}

func InitWorkerDatabaseConfig() {
	LoadConfig()
	if AppConfig == nil {
		log.Fatal("application configuration is not initialized")
	}
	pools, err := runtimeDatabasePoolAllocation(RuntimeRoleWorker)
	if err != nil {
		log.Fatalf("failed to configure worker database pool: %v", err)
	}
	db, err := openWorkerDatabase(DatabaseDSN(), pools.Worker)
	if err != nil {
		log.Fatalf("failed to initialize worker database: %v", err)
	}
	global.WorkerDb = db
	logDatabaseProfile("worker", db, pools.Worker)
}

func InitMaintenanceDatabaseConfig() {
	LoadConfig()
	if AppConfig == nil {
		log.Fatal("application configuration is not initialized")
	}
	pool := DatabasePoolOptions{
		MaxOpenConns: AppConfig.Database.MaxOpenConns,
		MaxIdleConns: AppConfig.Database.MaxIdleconns,
	}
	if override, set, err := databasePoolSizeOverride("MAINTENANCE_DB_MAX_OPEN_CONNS"); err != nil {
		log.Fatalf("failed to configure maintenance database pool: %v", err)
	} else if set {
		if override > pool.MaxOpenConns {
			log.Fatalf("MAINTENANCE_DB_MAX_OPEN_CONNS (%d) exceeds total database pool budget (%d)", override, pool.MaxOpenConns)
		}
		pool.MaxOpenConns = override
		if pool.MaxIdleConns > override {
			pool.MaxIdleConns = override
		}
	}
	db, err := openMaintenanceDatabase(DatabaseDSN(), pool)
	if err != nil {
		log.Fatalf("failed to initialize maintenance database: %v", err)
	}
	global.MaintenanceDb = db
	logDatabaseProfile("maintenance", db, pool)
}

func CloseDatabasePools() {
	handles := []*gorm.DB{global.APIDb, global.WorkerDb, global.MaintenanceDb}
	for _, db := range handles {
		if db == nil {
			continue
		}
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
	global.Db = nil
	global.APIDb = nil
	global.WorkerDb = nil
	global.MaintenanceDb = nil
}

func openRuntimeDatabaseHandles(dsn, role string, pools DatabasePoolAllocation) (*gorm.DB, *gorm.DB, error) {
	var apiDB, workerDB *gorm.DB
	if role == RuntimeRoleAPI || role == RuntimeRoleAll {
		db, err := openAPIDatabase(dsn, pools.API)
		if err != nil {
			return nil, nil, fmt.Errorf("open API database: %w", err)
		}
		apiDB = db
	}
	if role == RuntimeRoleWorker || role == RuntimeRoleAll {
		db, err := openWorkerDatabase(dsn, pools.Worker)
		if err != nil {
			closeDatabaseHandle(apiDB)
			return nil, nil, fmt.Errorf("open worker database: %w", err)
		}
		workerDB = db
	}
	return apiDB, workerDB, nil
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

func runtimeDatabasePoolAllocation(role string) (DatabasePoolAllocation, error) {
	if AppConfig == nil {
		return DatabasePoolAllocation{}, errors.New("application configuration is not initialized")
	}
	return allocateDatabasePools(
		role,
		AppConfig.Database.MaxOpenConns,
		AppConfig.Database.MaxIdleconns,
	)
}

func allocateDatabasePools(role string, totalOpen, totalIdle int) (DatabasePoolAllocation, error) {
	if totalOpen <= 0 {
		return DatabasePoolAllocation{}, errors.New("database max open connection budget must be positive")
	}
	if totalIdle < 0 {
		return DatabasePoolAllocation{}, errors.New("database max idle connection budget must not be negative")
	}
	if totalIdle > totalOpen {
		totalIdle = totalOpen
	}

	switch role {
	case RuntimeRoleAPI:
		apiOpen, err := requestedPoolSize("API_DB_MAX_OPEN_CONNS", totalOpen, totalOpen)
		if err != nil {
			return DatabasePoolAllocation{}, err
		}
		return DatabasePoolAllocation{API: DatabasePoolOptions{MaxOpenConns: apiOpen, MaxIdleConns: min(totalIdle, apiOpen)}}, nil
	case RuntimeRoleWorker:
		workerOpen, err := requestedPoolSize("WORKER_DB_MAX_OPEN_CONNS", totalOpen, totalOpen)
		if err != nil {
			return DatabasePoolAllocation{}, err
		}
		return DatabasePoolAllocation{Worker: DatabasePoolOptions{MaxOpenConns: workerOpen, MaxIdleConns: min(totalIdle, workerOpen)}}, nil
	case RuntimeRoleAll:
		apiOverride, apiSet, err := databasePoolSizeOverride("API_DB_MAX_OPEN_CONNS")
		if err != nil {
			return DatabasePoolAllocation{}, err
		}
		workerOverride, workerSet, err := databasePoolSizeOverride("WORKER_DB_MAX_OPEN_CONNS")
		if err != nil {
			return DatabasePoolAllocation{}, err
		}
		apiOpen, workerOpen := 0, 0
		switch {
		case apiSet && workerSet:
			apiOpen, workerOpen = apiOverride, workerOverride
		case apiSet:
			apiOpen, workerOpen = apiOverride, totalOpen-apiOverride
		case workerSet:
			workerOpen, apiOpen = workerOverride, totalOpen-workerOverride
		default:
			apiOpen = (totalOpen*70 + 99) / 100
			workerOpen = totalOpen - apiOpen
		}
		if apiOpen <= 0 || workerOpen <= 0 {
			return DatabasePoolAllocation{}, errors.New("runtime=all requires positive API and worker database pool sizes")
		}
		if apiOpen+workerOpen > totalOpen {
			return DatabasePoolAllocation{}, fmt.Errorf("API and worker max open connections (%d) exceed total database pool budget (%d)", apiOpen+workerOpen, totalOpen)
		}
		idleBudget := min(totalIdle, apiOpen+workerOpen)
		apiIdle := (idleBudget*apiOpen + (apiOpen+workerOpen)/2) / (apiOpen + workerOpen)
		apiIdle = min(apiIdle, apiOpen)
		workerIdle := min(idleBudget-apiIdle, workerOpen)
		return DatabasePoolAllocation{
			API:    DatabasePoolOptions{MaxOpenConns: apiOpen, MaxIdleConns: apiIdle},
			Worker: DatabasePoolOptions{MaxOpenConns: workerOpen, MaxIdleConns: workerIdle},
		}, nil
	default:
		return DatabasePoolAllocation{}, fmt.Errorf("unsupported database runtime role %q", role)
	}
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

func closeDatabaseHandle(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
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
