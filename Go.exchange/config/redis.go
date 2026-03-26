package config

import (
	"Go.exchange/global"
	"log"

	"github.com/go-redis/redis/v7"
)

func initRedis() {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         RedisAddr(),
		DB:           RedisDB(),
		Password:     RedisPassword(),
		PoolSize:     RedisPoolSize(),
		MinIdleConns: RedisMinIdleConns(),
	})
	_, err := redisClient.Ping().Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis, got error:%v", err)
	}
	global.RedisDB = redisClient
}
