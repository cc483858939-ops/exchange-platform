package main

import (
	"aceld/config"
	"aceld/global"
	"aceld/models"
	"aceld/router"
	"aceld/tasks"
	"log"
)

func main() {
	config.InitConfig()

	if err := global.Db.AutoMigrate(&models.User{}); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}
	log.Println("Database migration completed successfully.")

	r := router.SetupRouter()

	port := config.AppConfig.App.Port
	if port == "" {
		port = ":3000"
	}
	go tasks.SyncLikesToMySQL()
	log.Printf("Server is starting on port %s", port)
	if err := r.Run(port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
