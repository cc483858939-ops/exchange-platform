package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

const unavailableMinIOEndpoint = "http://127.0.0.1:1"

type workerBootstrapTestCalls struct {
	loadConfig      int
	workerDatabase  int
	redisClient     int
	closeDatabase   int
	startTasks      int
	startHealth     int
	waitForShutdown int
	loadedConfig    *config.Config
	redisFailure    error
}

func TestWorkerBootstrapDoesNotRequireMinIO(t *testing.T) {
	assertWorkerBootstrapSucceedsWithoutMinIOOrJWT(t)
}

func TestWorkerBootstrapDoesNotRequireJWT(t *testing.T) {
	assertWorkerBootstrapSucceedsWithoutMinIOOrJWT(t)
}

func TestWorkerBootstrapClosesDatabaseAndSkipsRuntimeStartWhenRedisFails(t *testing.T) {
	prepareWorkerBootstrapTest(t)

	calls := &workerBootstrapTestCalls{redisFailure: errors.New("injected Redis failure")}
	var redisClient *redis.Client
	t.Cleanup(func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	})
	deps := workerBootstrapTestDependencies(calls, &redisClient)

	err := runWithDependencies(deps)
	if !errors.Is(err, calls.redisFailure) {
		t.Fatalf("run error=%v, want Redis failure %v", err, calls.redisFailure)
	}
	if calls.loadConfig != 1 || calls.workerDatabase != 1 || calls.redisClient != 1 {
		t.Fatalf("bootstrap calls=%+v, want config, Worker DB, and Redis once", calls)
	}
	if calls.closeDatabase != 1 {
		t.Fatalf("database close calls=%d, want 1 after Redis failure", calls.closeDatabase)
	}
	if calls.startTasks != 0 || calls.startHealth != 0 || calls.waitForShutdown != 0 {
		t.Fatalf("runtime started after failed bootstrap: calls=%+v", calls)
	}
	if global.APIDb != nil || global.WorkerDb != nil || global.MaintenanceDb != nil || global.RedisDB != nil || global.MinioClient != nil {
		t.Fatalf("globals published after failed bootstrap: APIDb=%p WorkerDb=%p MaintenanceDb=%p RedisDB=%p MinioClient=%p",
			global.APIDb, global.WorkerDb, global.MaintenanceDb, global.RedisDB, global.MinioClient)
	}
}

func assertWorkerBootstrapSucceedsWithoutMinIOOrJWT(t *testing.T) {
	t.Helper()
	prepareWorkerBootstrapTest(t)

	calls := &workerBootstrapTestCalls{}
	var redisClient *redis.Client
	t.Cleanup(func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	})
	deps := workerBootstrapTestDependencies(calls, &redisClient)

	runtime, err := bootstrapWorkerWithDependencies(deps)
	if err != nil {
		t.Fatalf("Worker bootstrap with MinIO and JWT absent: %v", err)
	}
	if calls.loadConfig != 1 || calls.workerDatabase != 1 || calls.redisClient != 1 {
		t.Fatalf("bootstrap calls=%+v, want config, Worker DB, and Redis once", calls)
	}
	if runtime.database == nil || runtime.redisClient == nil {
		t.Fatalf("runtime dependencies: database=%p redis=%p", runtime.database, runtime.redisClient)
	}
	if calls.loadedConfig == nil || calls.loadedConfig.Storage.Endpoint != unavailableMinIOEndpoint ||
		calls.loadedConfig.Storage.AccessKey != "" || calls.loadedConfig.Storage.SecretKey != "" {
		t.Fatalf("test config unexpectedly needs MinIO: %#v", calls.loadedConfig.Storage)
	}
	if global.APIDb != nil || global.WorkerDb != runtime.database || global.MaintenanceDb != nil ||
		global.RedisDB != runtime.redisClient || global.MinioClient != nil {
		t.Fatalf("unexpected Worker globals: APIDb=%p WorkerDb=%p MaintenanceDb=%p RedisDB=%p MinioClient=%p",
			global.APIDb, global.WorkerDb, global.MaintenanceDb, global.RedisDB, global.MinioClient)
	}
}

func prepareWorkerBootstrapTest(t *testing.T) {
	t.Helper()
	t.Chdir("../..")
	for _, name := range []string{
		"MINIO_ACCESS_KEY",
		"MINIO_SECRET_KEY",
		"MINIO_BUCKET",
		"MINIO_USE_SSL",
		"JWT_PRIVATE_KEY_FILE",
		"JWT_VERIFY_KEYS_DIR",
		"JWT_ACTIVE_KID",
		"JWT_ISSUER",
		"JWT_AUDIENCE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("MINIO_ENDPOINT", unavailableMinIOEndpoint)

	oldAPIDb := global.APIDb
	oldWorkerDb := global.WorkerDb
	oldMaintenanceDb := global.MaintenanceDb
	oldRedisDB := global.RedisDB
	oldMinioClient := global.MinioClient
	oldAppConfig := config.AppConfig
	global.APIDb = nil
	global.WorkerDb = nil
	global.MaintenanceDb = nil
	global.RedisDB = nil
	global.MinioClient = nil
	config.AppConfig = nil
	t.Cleanup(func() {
		global.APIDb = oldAPIDb
		global.WorkerDb = oldWorkerDb
		global.MaintenanceDb = oldMaintenanceDb
		global.RedisDB = oldRedisDB
		global.MinioClient = oldMinioClient
		config.AppConfig = oldAppConfig
	})
}

func workerBootstrapTestDependencies(calls *workerBootstrapTestCalls, redisClientOut **redis.Client) workerBootstrapDeps {
	deps := defaultWorkerBootstrapDeps()
	deps.loadConfig = func() (*config.Config, error) {
		calls.loadConfig++
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		cfg.Storage.Endpoint = unavailableMinIOEndpoint
		cfg.Storage.AccessKey = ""
		cfg.Storage.SecretKey = ""
		calls.loadedConfig = cfg
		return cfg, nil
	}
	deps.openWorkerDatabase = func(cfg *config.Config) (*gorm.DB, error) {
		calls.workerDatabase++
		if cfg == nil {
			return nil, errors.New("Worker config is nil")
		}
		return &gorm.DB{}, nil
	}
	deps.newRedisClient = func() (*redis.Client, error) {
		calls.redisClient++
		if calls.redisFailure != nil {
			return nil, calls.redisFailure
		}
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		*redisClientOut = client
		return client, nil
	}
	deps.closeDatabase = func(*gorm.DB) error {
		calls.closeDatabase++
		return nil
	}
	deps.startTasks = func(context.Context, *sync.WaitGroup) {
		calls.startTasks++
	}
	deps.startHealthServer = func() *http.Server {
		calls.startHealth++
		return &http.Server{}
	}
	deps.waitForShutdown = func(context.CancelFunc, *sync.WaitGroup) {
		calls.waitForShutdown++
	}
	return deps
}
