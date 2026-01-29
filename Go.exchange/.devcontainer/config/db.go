package config

import (
	"Go.exchange/global"
	"log"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func initDB() {
	dsn := AppConfig.Database.Dsn
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to initialize datebase,got erro:%v", err)
	}
	sqlDB, err := db.DB()
	sqlDB.SetMaxIdleConns(AppConfig.Database.MaxIdleconns)
	sqlDB.SetMaxOpenConns(AppConfig.Database.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)
	if err != nil {
		log.Fatalf("Failed to configure database, got erro:%v", err)
	}
	global.Db = db
}

// 参考 https://github.com/go-sql-driver/mysql#dsn-data-source-name
