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
		log.Fatalf("failed to migrate database: %v", err)
	}
}

func run() error {
	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := initialize.RunMigrationsWithDB(ctx, global.MaintenanceDb); err != nil {
		return err
	}
	log.Println("database migration completed")
	return nil
}
