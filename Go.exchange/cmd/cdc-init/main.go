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
	config.InitDatabaseConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := global.Db.DB()
	if err != nil {
		log.Fatalf("open CDC database connection: %v", err)
	}
	connectURL, databaseUser, databasePassword := cdc.Environment()
	status, err := cdc.Run(ctx, db, config.AppConfig.Kafka, connectURL, databaseUser, databasePassword)
	if err != nil {
		log.Fatalf("CDC initialization failed: %v", err)
	}
	log.Printf("CDC ready: publication=%s slot=%s connector=%s state=%s tasks=%d", cdc.PublicationName, cdc.SlotName, cdc.ConnectorName, status.Connector.State, len(status.Tasks))
}
