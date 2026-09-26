package global

import (
	"github.com/go-redis/redis/v7"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

var (
	// Db remains the API database alias for existing controller and service code.
	Db *gorm.DB

	// APIDb, WorkerDb, and MaintenanceDb use separate pools and startup timeout
	// settings. Db must remain an alias of APIDb.
	APIDb         *gorm.DB
	WorkerDb      *gorm.DB
	MaintenanceDb *gorm.DB

	RedisDB     *redis.Client
	MinioClient *minio.Client
)
