package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/initialize"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("outbox cutover failed: %v", err)
	}
}

func run() error {
	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := initialize.CutoverLegacyOutboxWithContext(ctx, global.MaintenanceDb, initialize.OutboxCutoverConfirmed()); err != nil {
		return err
	}
	log.Println("outbox cutover completed or was not required")
	return nil
}
