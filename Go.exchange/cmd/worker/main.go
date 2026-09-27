package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/core"
	"Go.exchange/global"
	"Go.exchange/tasks"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	return runWithDependencies(defaultWorkerBootstrapDeps())
}

type workerBootstrapDeps struct {
	loadConfig         func() (*config.Config, error)
	validateConfig     func(*config.Config) error
	openWorkerDatabase func(*config.Config) (*gorm.DB, error)
	newRedisClient     func() (*redis.Client, error)
	closeDatabase      func(*gorm.DB) error
	closeRedisClient   func(*redis.Client) error
	startTasks         func(context.Context, *sync.WaitGroup)
	startHealthServer  func() *http.Server
	waitForShutdown    func(context.CancelFunc, *sync.WaitGroup)
}

type workerRuntime struct {
	database    *gorm.DB
	redisClient *redis.Client
}

func defaultWorkerBootstrapDeps() workerBootstrapDeps {
	return workerBootstrapDeps{
		loadConfig:         config.Load,
		validateConfig:     config.ValidateWorkerEventingConfig,
		openWorkerDatabase: config.OpenWorkerDatabase,
		newRedisClient:     config.NewRedisClient,
		closeDatabase:      config.CloseDatabase,
		closeRedisClient:   func(client *redis.Client) error { return client.Close() },
		startTasks:         tasks.StartAll,
		startHealthServer: func() *http.Server {
			return core.StartWorkerHealthServer(config.WorkerHealthAddr(), tasks.WorkerReadinessSnapshot)
		},
		waitForShutdown: waitForWorkerShutdown,
	}
}

func bootstrapWorkerWithDependencies(deps workerBootstrapDeps) (*workerRuntime, error) {
	cfg, err := deps.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load Worker configuration: %w", err)
	}
	if err := deps.validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate Worker configuration: %w", err)
	}

	db, err := deps.openWorkerDatabase(cfg)
	if err != nil {
		return nil, fmt.Errorf("open Worker database: %w", err)
	}
	if db == nil {
		return nil, errors.New("open Worker database returned nil")
	}

	redisClient, err := deps.newRedisClient()
	if err != nil {
		bootstrapErr := fmt.Errorf("create Worker Redis client: %w", err)
		if closeErr := deps.closeDatabase(db); closeErr != nil {
			return nil, errors.Join(bootstrapErr, fmt.Errorf("close Worker database after Redis failure: %w", closeErr))
		}
		return nil, bootstrapErr
	}
	if redisClient == nil {
		bootstrapErr := errors.New("create Worker Redis client returned nil")
		if closeErr := deps.closeDatabase(db); closeErr != nil {
			return nil, errors.Join(bootstrapErr, fmt.Errorf("close Worker database after Redis failure: %w", closeErr))
		}
		return nil, bootstrapErr
	}

	global.Db = nil
	global.APIDb = nil
	global.WorkerDb = db
	global.RedisDB = redisClient
	return &workerRuntime{database: db, redisClient: redisClient}, nil
}

func runWithDependencies(deps workerBootstrapDeps) error {
	runtime, err := bootstrapWorkerWithDependencies(deps)
	if err != nil {
		return err
	}
	defer func() {
		if err := deps.closeDatabase(runtime.database); err != nil {
			log.Printf("close Worker database: %v", err)
		}
	}()
	defer func() {
		if err := deps.closeRedisClient(runtime.redisClient); err != nil {
			log.Printf("close Worker Redis client: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waitGroup sync.WaitGroup
	deps.startTasks(ctx, &waitGroup)
	healthServer := deps.startHealthServer()
	deps.waitForShutdown(cancel, &waitGroup)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("close Worker health server: %v", err)
	}
	return nil
}

func waitForWorkerShutdown(cancel context.CancelFunc, waitGroup *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	<-quit
	tasks.MarkWorkerShuttingDown()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("worker tasks stopped")
	case <-shutdownCtx.Done():
		log.Println("worker shutdown timed out")
	}
}
