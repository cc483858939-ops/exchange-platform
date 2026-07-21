package global

import (
	"github.com/go-redis/redis/v7"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

var (
	Db          *gorm.DB
	RedisDB     *redis.Client
	MinioClient *minio.Client
)
