package core

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"Go.exchange/metrics"
	"Go.exchange/runtimehealth"
)

func StartWorkerHealthServer(addr string, snapshotProvider func() runtimehealth.WorkerReadinessSnapshot) *http.Server {
	server := newWorkerHealthServer(addr, workerHealthHandler(snapshotProvider))
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("worker health server: %v", err)
		}
	}()
	return server
}

func newWorkerHealthServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

func workerHealthHandler(snapshotProvider func() runtimehealth.WorkerReadinessSnapshot) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/json")
		statusCode := http.StatusServiceUnavailable
		var snapshot runtimehealth.WorkerReadinessSnapshot
		if snapshotProvider != nil {
			snapshot = snapshotProvider()
			if snapshot.Status == "ready" {
				statusCode = http.StatusOK
			}
		} else {
			snapshot = runtimehealth.WorkerReadinessSnapshot{
				Status:      "not_ready",
				Role:        "worker",
				Checks:      map[string]string{},
				Pipelines:   map[string]runtimehealth.WorkerPipelineSnapshot{},
				ReasonCodes: []string{"worker_readiness_unavailable"},
			}
		}
		writer.WriteHeader(statusCode)
		_ = json.NewEncoder(writer).Encode(snapshot)
	})
	mux.Handle("/metrics", metrics.Handler())
	return mux
}
