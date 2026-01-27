package config

import (
	"aceld/global"
	"log"

	"github.com/go-redis/redis/v7"
)

func initRedis() {
	RedisClient := redis.NewClient(&redis.Options{
		Addr:     "redis:6379",
		DB:       0,
		Password: "",
	})
	_, err := RedisClient.Ping().Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis, got error:%v", err)
	}
	global.RedisDB = RedisClient
}
