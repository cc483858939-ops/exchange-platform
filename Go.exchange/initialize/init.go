package initialize

import (
	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
	"log"
)

func InitAll() {
	config.InitConfig()
	if err := global.Db.AutoMigrate(&models.User{}, &models.Article{}); err != nil {
		// 如果这里报错，说明连数据库都连不上或者表建不了，直接让程序退出
		log.Fatalf("failed to migrate database: %v", err)
	}
}
