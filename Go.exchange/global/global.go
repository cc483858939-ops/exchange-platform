package global

import (
	"github.com/go-redis/redis/v7"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

var (
	// APIDb, WorkerDb, and MaintenanceDb use separate pools and startup timeout
	// settings.
	APIDb         *gorm.DB
	WorkerDb      *gorm.DB
	MaintenanceDb *gorm.DB

	RedisDB     *redis.Client
	MinioClient *minio.Client
)
