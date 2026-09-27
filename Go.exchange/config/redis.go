package config

import (
	"github.com/go-redis/redis/v7"
)

// NewRedisClient connects and verifies Redis. Runtime composition roots own
// failure handling and closing the returned client.
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
