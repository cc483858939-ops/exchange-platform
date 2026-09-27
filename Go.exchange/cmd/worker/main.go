package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/core"
	"Go.exchange/global"
	"Go.exchange/tasks"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := config.ValidateWorkerEventingConfig(cfg); err != nil {
		return err
	}

	db, err := config.OpenWorkerDatabase(cfg)
	if err != nil {
		return err
	}
	global.Db = nil
	global.APIDb = nil
	global.WorkerDb = db
	defer func() {
		if err := config.CloseDatabase(db); err != nil {
			log.Printf("close Worker database: %v", err)
		}
	}()

	redisClient, err := config.NewRedisClient()
	if err != nil {
		return err
	}
	global.RedisDB = redisClient
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("close Worker Redis client: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waitGroup sync.WaitGroup
	tasks.StartAll(ctx, &waitGroup)
	healthServer := core.StartWorkerHealthServer(config.WorkerHealthAddr(), tasks.WorkerReadinessSnapshot)
	waitForWorkerShutdown(cancel, &waitGroup)

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
