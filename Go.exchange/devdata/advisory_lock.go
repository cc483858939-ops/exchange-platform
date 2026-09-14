package devdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"

	"gorm.io/gorm"
)

const DevDataMutationLockName = "devdata:x-mirror-db-mutator"

type DevDataMutationLock struct {
	conn     *sql.Conn
	key      int64
	released bool
}

func TryAcquireDevDataMutationLock(ctx context.Context, db *gorm.DB) (*DevDataMutationLock, bool, error) {
	return acquireDevDataMutationLock(ctx, db, true)
}

func AcquireDevDataMutationLock(ctx context.Context, db *gorm.DB) (*DevDataMutationLock, error) {
	lock, _, err := acquireDevDataMutationLock(ctx, db, false)
	return lock, err
}

func acquireDevDataMutationLock(ctx context.Context, db *gorm.DB, try bool) (*DevDataMutationLock, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, false, errors.New("database is not initialized")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, false, fmt.Errorf("get database connection pool: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("reserve DevData mutation lock connection: %w", err)
	}
	lock := &DevDataMutationLock{conn: conn, key: devDataMutationLockKey()}
	query := "SELECT pg_advisory_lock($1)"
	if try {
		query = "SELECT pg_try_advisory_lock($1)"
	}
	if try {
		var acquired bool
		if err := conn.QueryRowContext(ctx, query, lock.key).Scan(&acquired); err != nil {
			_ = conn.Close()
			return nil, false, fmt.Errorf("try acquire DevData mutation lock: %w", err)
		}
		if !acquired {
			_ = conn.Close()
			return nil, false, nil
		}
	} else if _, err := conn.ExecContext(ctx, query, lock.key); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("acquire DevData mutation lock: %w", err)
	}
	return lock, true, nil
}

func (lock *DevDataMutationLock) Release(ctx context.Context) error {
	if lock == nil || lock.released {
		return nil
	}
	lock.released = true
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	if lock.conn != nil {
		var released bool
		err = lock.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", lock.key).Scan(&released)
		if err == nil && !released {
			err = errors.New("DevData mutation lock was not held by this session")
		}
		if closeErr := lock.conn.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return fmt.Errorf("release DevData mutation lock: %w", err)
	}
	return nil
}

func DevDataMutationLockKey() int64 {
	return devDataMutationLockKey()
}

func devDataMutationLockKey() int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(DevDataMutationLockName))
	return int64(hasher.Sum64())
}
