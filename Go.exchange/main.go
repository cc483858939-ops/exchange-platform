package main

import (
	"Go.exchange/config"
	"Go.exchange/core"
	"Go.exchange/initialize"
	"Go.exchange/tasks"
	"context"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
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
	var wg sync.WaitGroup
	startPprof()
	log.Printf("starting go.exchange in %s mode", role)

	switch role {
	case config.RuntimeRoleAPI:
		startAPI(ctx, cancel, &wg)
	case config.RuntimeRoleWorker:
		startWorker(ctx, cancel, &wg)
	default:
		startAll(ctx, cancel, &wg)
	}
}

func startAPI(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup) {
	srv := core.StartHttpServer()
	core.WaitForShutdown(ctx, cancel, srv, wg)
}

func startWorker(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup) {
	tasks.StartAll(ctx, wg)
	waitForWorkerShutdown(cancel, wg)
}

func startAll(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup) {
	tasks.StartAll(ctx, wg)
	startAPI(ctx, cancel, wg)
}

func startPprof() {
	go func() {
		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			log.Println("Pprof error:", err)
		}
	}()
}

func waitForWorkerShutdown(cancel context.CancelFunc, wg *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	<-quit
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("worker tasks stopped")
	case <-shutdownCtx.Done():
		log.Println("worker shutdown timed out")
	}
}
