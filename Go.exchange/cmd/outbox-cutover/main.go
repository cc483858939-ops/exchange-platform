package main

import (
	"log"

	"Go.exchange/config"
	"Go.exchange/initialize"
)

func main() {
	config.InitDatabaseConfig()
	if err := initialize.RunOutboxCutover(); err != nil {
		log.Fatalf("outbox cutover failed: %v", err)
	}
	log.Println("outbox cutover completed or was not required")
}
