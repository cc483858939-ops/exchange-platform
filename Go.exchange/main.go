package main

import (
	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/core"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/tasks"
	"context"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = ioutil.Discard
	initialize.InitAll()

	role := config.RuntimeRole()
	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	log.Printf("starting go.exchange in %s mode", role)

	switch role {
	case config.RuntimeRoleAPI:
		startAPI(ctx, cancel, &waitGroup, mustTokenManager())
	case config.RuntimeRoleWorker:
		startWorker(ctx, cancel, &waitGroup)
	default:
		startAll(ctx, cancel, &waitGroup, mustTokenManager())
	}
}

func mustTokenManager() *auth.Manager {
	authConfig, err := auth.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid JWT configuration: %v", err)
	}
	refreshStore, err := auth.NewRedisRefreshStore(global.RedisDB)
	if err != nil {
		log.Fatalf("initialize refresh store: %v", err)
	}
	manager, err := auth.NewManager(authConfig, refreshStore)
	if err != nil {
		log.Fatalf("initialize token manager: %v", err)
	}
	log.Printf("JWT signer initialized with kid=%s", authConfig.ActiveKID)
	return manager
}

func startAPI(ctx context.Context, cancel context.CancelFunc, waitGroup *sync.WaitGroup, tokens auth.TokenService) {
	server, err := core.StartHttpServer(tokens)
	if err != nil {
		log.Fatalf("start HTTP server: %v", err)
	}
	core.WaitForShutdown(ctx, cancel, server, waitGroup)
}

func startWorker(ctx context.Context, cancel context.CancelFunc, waitGroup *sync.WaitGroup) {
	tasks.StartAll(ctx, waitGroup)
	healthServer := core.StartWorkerHealthServer(config.WorkerHealthAddr(), tasks.WorkerReady)
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()
	waitForWorkerShutdown(cancel, waitGroup)
}

func startAll(ctx context.Context, cancel context.CancelFunc, waitGroup *sync.WaitGroup, tokens auth.TokenService) {
	tasks.StartAll(ctx, waitGroup)
	startAPI(ctx, cancel, waitGroup, tokens)
}

func waitForWorkerShutdown(cancel context.CancelFunc, waitGroup *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	<-quit
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
