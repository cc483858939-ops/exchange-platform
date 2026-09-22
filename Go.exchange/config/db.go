package config

import (
	"Go.exchange/global"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func initDB() {
	dsn := DatabaseDSN()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to initialize datebase,got erro:%v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to configure database, got error: %v", err)
	}
	sqlDB.SetMaxIdleConns(AppConfig.Database.MaxIdleconns)
	sqlDB.SetMaxOpenConns(AppConfig.Database.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)
	global.Db = db
}

// Reference: https://github.com/go-gorm/postgres
