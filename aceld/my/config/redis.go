package config

import (
	"aceld/global"
	"log"

	"github.com/go-redis/redis/v7"
)

func initRedis() {
	RedisClient := redis.NewClient(&redis.Options{
		Addr:         "redis:6379",
		DB:           0,
		Password:     "",
		PoolSize:     1000, // 核心：将连接池扩大到 1000 (默认是 10，高并发下会导致严重的锁等待)
		MinIdleConns: 50,
	})
	_, err := RedisClient.Ping().Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis, got error:%v", err)
	}
	global.RedisDB = RedisClient
}
