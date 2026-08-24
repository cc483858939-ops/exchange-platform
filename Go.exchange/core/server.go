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
	"Go.exchange/initialize"
	"Go.exchange/router"
	"Go.exchange/runtimehealth"
)

var httpReadinessByServer sync.Map

func StartHttpServer(tokens auth.TokenService, publisher eventing.BatchPublisher) (*http.Server, error) {
	limiter, err := auth.NewRedisAttemptLimiter(global.RedisDB)
	if err != nil {
		return nil, fmt.Errorf("initialize auth rate limiter: %w", err)
	}
	authController, err := controllers.NewAuthController(global.Db, tokens, limiter)
	if err != nil {
		return nil, fmt.Errorf("initialize auth controller: %w", err)
	}
	port := config.AppPort()
	readiness := runtimehealth.NewAPIReadiness(runtimehealth.APIOptions{
		Role:                  config.RuntimeRoleAPI,
		RequiredSchemaVersion: initialize.RequiredSchemaVersion,
		EmbeddingEnabled:      config.AppConfig != nil && config.AppConfig.Embedding.Enabled,
	})
	handler, err := router.SetupRouter(authController, tokens, publisher, readiness)
	if err != nil {
		return nil, fmt.Errorf("initialize HTTP router: %w", err)
	}
	readiness.Start(context.Background())
	server := newAPIServer(port, handler)
	httpReadinessByServer.Store(server, readiness)
	server.RegisterOnShutdown(readiness.Stop)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen: %s\n", err)
		}
	}()
	return server, nil
}

func newAPIServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func WaitForShutdown(ctx context.Context, cancel context.CancelFunc, server *http.Server, waitGroup *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	if value, ok := httpReadinessByServer.Load(server); ok {
		readiness := value.(*runtimehealth.APIReadiness)
		readiness.MarkShuttingDown()
		readiness.Stop()
		httpReadinessByServer.Delete(server)
	}
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
