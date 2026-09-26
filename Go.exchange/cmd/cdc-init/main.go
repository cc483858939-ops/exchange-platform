package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"Go.exchange/cdc"
	"Go.exchange/config"
	"Go.exchange/global"
)

func main() {
	config.InitMaintenanceDatabaseConfig()
	defer config.CloseDatabasePools()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := global.MaintenanceDb.DB()
	if err != nil {
		log.Fatalf("open CDC database connection: %v", err)
	}
	environment := cdc.Environment()
	source, err := cdc.ParseSourceDatabaseConfig(config.DatabaseDSN(), environment)
	if err != nil {
		log.Fatalf("CDC source database configuration failed: %v", err)
	}
	status, err := cdc.Run(ctx, db, config.AppConfig.Kafka, environment.ConnectURL, source)
	if err != nil {
		log.Fatalf("CDC initialization failed: %v", err)
	}
	log.Printf("CDC ready: publication=%s slot=%s connector=%s state=%s tasks=%d", cdc.PublicationName, cdc.SlotName, cdc.ConnectorName, status.Connector.State, len(status.Tasks))
}
