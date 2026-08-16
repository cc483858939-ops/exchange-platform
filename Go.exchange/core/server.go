package core

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/router"
)

func StartHttpServer(tokens auth.TokenService, publisher eventing.BatchPublisher) (*http.Server, error) {
	authController, err := controllers.NewAuthController(global.Db, tokens)
	if err != nil {
		return nil, fmt.Errorf("initialize auth controller: %w", err)
	}
	port := config.AppPort()
	handler := router.SetupRouter(authController, tokens, publisher)
	server := &http.Server{
		Addr:    port,
		Handler: handler,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen: %s\n", err)
		}
	}()
	return server, nil
}

func WaitForShutdown(ctx context.Context, cancel context.CancelFunc, server *http.Server, waitGroup *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server forced to shut down: %v", err)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Println("background tasks stopped")
	case <-shutdownCtx.Done():
		log.Println("background task shutdown timed out")
	}
}
