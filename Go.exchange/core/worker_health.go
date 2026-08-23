package core

import (
	"log"
	"net/http"

	"Go.exchange/metrics"
)

func StartWorkerHealthServer(addr string, ready func() bool) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if ready != nil && ready() {
			writer.WriteHeader(http.StatusOK)
			return
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.Handle("/metrics", metrics.Handler())

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("worker health server: %v", err)
		}
	}()
	return server
}
