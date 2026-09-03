package controllers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"golang.org/x/sync/singleflight"
)

// postCacheTTL defines the lifetime of the viewer-independent Post detail cache.
const postCacheTTL = 10 * time.Minute

var (
	// postCacheGroup 用于防击穿的 Singleflight 分组
	postCacheGroup singleflight.Group
)

// cacheGetter 定义从缓存获取数据的函数签名
type cacheGetter func(key string) (string, error)

// cacheSetter 定义将数据写入缓存的函数签名
type cacheSetter func(key string, payload []byte, expiration time.Duration) error

// postDetailCacheKey returns the canonical Post detail key.
func postDetailCacheKey(id string) string {
	return "post:detail:v3:" + id
}

// InvalidatePostDetailCacheByIDWithRedis deletes the canonical viewer-
// independent Post detail cache key using an explicitly supplied Redis
// client. DevData uses this helper so it does not need to mutate the API's
// global Redis client just to perform post-commit maintenance.
func InvalidatePostDetailCacheByIDWithRedis(client *redis.Client, id uint) error {
	if client == nil {
		return nil
	}
	return client.Del(postDetailCacheKey(strconv.FormatUint(uint64(id), 10))).Err()
}

// InvalidatePostDetailCacheByID 主动删除指定文章的详情缓存。
func InvalidatePostDetailCacheByID(id uint) error {
	return InvalidatePostDetailCacheByIDWithRedis(global.RedisDB, id)
}

// loadJSONCache 默认使用全局 Redis 的缓存包装函数
func loadJSONCache[T any](key string, loader func() (T, error)) (T, error) {
	if global.RedisDB == nil {
		return loader()
	}
	return loadJSONCacheWithStore(
		key,
		postCacheTTL,
		func(key string) (string, error) {
			return global.RedisDB.Get(key).Result()
		},
		func(key string, payload []byte, expiration time.Duration) error {
			return global.RedisDB.Set(key, payload, expiration).Err()
		},
		loader,
	)
}

// loadJSONCacheWithStore 核心缓存加载逻辑，集成了 Singleflight 防击穿机制
func loadJSONCacheWithStore[T any](
	key string,
	expiration time.Duration,
	getter cacheGetter,
	setter cacheSetter,
	loader func() (T, error),
) (T, error) {
	var zero T

	// 1. 第一层检查：直接尝试从缓存获取
	cachedData, err := getter(key)
	switch {
	case err == nil:
		// 命中了直接反序列化返回
		return unmarshalCachedValue[T](cachedData)
	case err != redis.Nil:
		// 遇到非 Nil 错误（如 Redis 挂了），直接返回错误，不建议盲目穿透
		return zero, err
	}

	// 2. 缓存未命中，进入 Singleflight 控制
	value, err, _ := postCacheGroup.Do(key, func() (interface{}, error) {
		// 2.1 二次检查：
		// 当并发请求被 Do 阻塞再被唤醒时，之前的请求可能已经把缓存填上了，
		// 所以在这里再查一次 Redis，如果命中了直接返回，避免再次打库。
		cachedData, err := getter(key)
		switch {
		case err == nil:
			return unmarshalCachedValue[T](cachedData)
		case err != redis.Nil:
			return zero, err
		}

		// 2.2 真正的回源加载：查数据库
		result, err := loader()
		if err != nil {
			return zero, err
		}

		// 2.3 序列化并回填缓存
		payload, err := json.Marshal(result)
		if err != nil {
			return zero, err
		}
		if err := setter(key, payload, expiration); err != nil {
			// Redis 出现问题时，直接上报错误，避免高并发把数据库压垮。
			return zero, err
		}
		return result, nil
	})
	if err != nil {
		return zero, err
	}

	// 3. 将 Do 返回的 interface{} 转换为具体类型
	result, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("unexpected cache result type for key %s", key)
	}
	return result, nil
}

// unmarshalCachedValue 辅助函数：反序列化缓存字符串
func unmarshalCachedValue[T any](cachedData string) (T, error) {
	var result T
	if err := json.Unmarshal([]byte(cachedData), &result); err != nil {
		return result, err
	}
	return result, nil
}
