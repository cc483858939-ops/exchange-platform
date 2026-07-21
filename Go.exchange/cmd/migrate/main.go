package main

import (
	"log"

	"Go.exchange/config"
	"Go.exchange/initialize"
)

func main() {
	config.InitDatabaseConfig()

	if err := initialize.RunMigrations(); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}

	log.Println("database migration completed")
}
