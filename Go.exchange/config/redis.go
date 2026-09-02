package config

import (
	"Go.exchange/global"
	"log"

	"github.com/go-redis/redis/v7"
)

func InitRedis() { initRedis() }

// NewRedisClient connects and verifies Redis without terminating the process.
// CLI tooling uses this best-effort path; production startup continues to use
// initRedis below and retains its existing fatal-on-failure behavior.
func NewRedisClient() (*redis.Client, error) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         RedisAddr(),
		DB:           RedisDB(),
		Password:     RedisPassword(),
		PoolSize:     RedisPoolSize(),
		MinIdleConns: RedisMinIdleConns(),
	})
	_, err := redisClient.Ping().Result()
	if err != nil {
		_ = redisClient.Close()
		return nil, err
	}
	return redisClient, nil
}

func initRedis() {
	redisClient, err := NewRedisClient()
	if err != nil {
		log.Fatalf("Failed to connect to Redis, got error:%v", err)
	}
	global.RedisDB = redisClient
}
