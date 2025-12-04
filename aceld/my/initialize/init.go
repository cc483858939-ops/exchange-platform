package initialize

import (
	"aceld/config"
	"aceld/global"
	"aceld/models"
	"log"
)

func InitAll() {
	config.InitConfig()
	if err := global.Db.AutoMigrate(&models.User{}); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}
}
