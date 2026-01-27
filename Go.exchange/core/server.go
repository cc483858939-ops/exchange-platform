package core

import (
	"aceld/config"
	"aceld/router"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func StartHttpServer() *http.Server {
	port := config.AppConfig.App.Port
	if port == "" {
		port = ":3000"
	}
	r := router.SetupRouter()
	srv := &http.Server{
		Addr:    port,
		Handler: r,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen: %s\n", err)
		}

	}()
	return srv
}
func WaitForShutdown(ctx context.Context, cancel context.CancelFunc, srv *http.Server, wg *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	shutdowCtx, shutdowCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdowCancel()
	if err := srv.Shutdown(shutdowCtx); err != nil {
		log.Printf("服务强制关闭")
	}
	cancel()
	doneChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneChan)
	}()
	select {
	case <-doneChan:
		log.Println("任务执行完成")
	case <-shutdowCtx.Done():
		log.Printf("执行时间过长强制退出")
	}
}
