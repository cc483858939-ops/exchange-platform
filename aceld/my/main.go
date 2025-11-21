package main

import (
	"aceld/config" // <-- 已修改，去掉了 demon2
	"aceld/global" // <-- 已修改，去掉了 demon2
	"aceld/models" // <-- 已修改，去掉了 demon2
	"aceld/router" // <-- 已修改，去掉了 demon2
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

	log.Printf("Server is starting on port %s", port)
	if err := r.Run(port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
