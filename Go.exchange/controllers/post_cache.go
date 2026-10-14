package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// postCacheTTL defines the lifetime of the viewer-independent Post detail cache.
const postCacheTTL = 10 * time.Minute

// postDetailFenceTTL bounds the lifetime of the temporary write fence.
const postDetailFenceTTL = 20 * time.Minute

const postDetailCacheKeyPrefix = "post:detail:v5:"

const postDetailFenceKeyPrefix = "post:detail:fence:"

const initializePostDetailFenceScript = `
local current = redis.call("GET", KEYS[1])
if current then
    return current
end
local initialized = redis.call("SET", KEYS[1], ARGV[1], "NX", "EX", ARGV[2])
if initialized then
    return ARGV[1]
end
return redis.call("GET", KEYS[1])
`

const invalidatePostDetailCacheScript = `
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
redis.call("DEL", KEYS[2])
return 1
`

const setPostDetailCacheIfFenceMatchesScript = `
local current = redis.call("GET", KEYS[1])
if current and current == ARGV[1] then
    redis.call("SET", KEYS[2], ARGV[2], "PX", ARGV[3])
    return 1
end
return 0
`

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
	return postDetailCacheKeyPrefix + id
}

func postDetailFenceKey(id string) string {
	return postDetailFenceKeyPrefix + id
}

func postDetailIDFromCacheKey(key string) (string, error) {
	id, ok := strings.CutPrefix(key, postDetailCacheKeyPrefix)
	if !ok || id == "" {
		return "", fmt.Errorf("invalid Post detail cache key %q", key)
	}
	return id, nil
}

func newPostDetailFenceToken() (string, error) {
	token, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate Post detail cache fence token: %w", err)
	}
	return token.String(), nil
}

func getOrCreatePostDetailFenceToken(ctx context.Context, client *redis.Client, id string) (string, error) {
	token, err := newPostDetailFenceToken()
	if err != nil {
		return "", err
	}
	value, err := client.WithContext(ctx).Eval(
		initializePostDetailFenceScript,
		[]string{postDetailFenceKey(id)},
		token,
		int64(postDetailFenceTTL/time.Second),
	).Text()
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("Post detail cache fence token is empty for Post %s", id)
	}
	return value, nil
}

func setPostDetailCacheIfFenceMatches(ctx context.Context, client *redis.Client, id, expectedToken string, payload []byte, expiration time.Duration) (bool, error) {
	result, err := client.WithContext(ctx).Eval(
		setPostDetailCacheIfFenceMatchesScript,
		[]string{postDetailFenceKey(id), postDetailCacheKey(id)},
		expectedToken,
		payload,
		expiration.Milliseconds(),
	).Int()
	if err != nil {
		return false, err
	}
	if result != 0 && result != 1 {
		return false, fmt.Errorf("unexpected Post detail cache fence result %d", result)
	}
	return result == 1, nil
}

func invalidatePostDetailCacheKeyWithRedis(client *redis.Client, cacheKey string) error {
	if client == nil {
		return nil
	}
	id, err := postDetailIDFromCacheKey(cacheKey)
	if err != nil {
		return err
	}
	token, err := newPostDetailFenceToken()
	if err != nil {
		return err
	}
	return client.Eval(
		invalidatePostDetailCacheScript,
		[]string{postDetailFenceKey(id), cacheKey},
		token,
		int64(postDetailFenceTTL/time.Second),
	).Err()
}

// InvalidatePostDetailCacheByIDWithRedis atomically rotates the write fence
// and deletes the canonical detail cache using the explicitly supplied Redis
// client. DevData uses this helper without changing the API's global client.
func InvalidatePostDetailCacheByIDWithRedis(client *redis.Client, id uint) error {
	if client == nil {
		return nil
	}
	return invalidatePostDetailCacheKeyWithRedis(client, postDetailCacheKey(strconv.FormatUint(uint64(id), 10)))
}

// InvalidatePostDetailCacheByID 主动删除指定文章的详情缓存。
func InvalidatePostDetailCacheByID(id uint) error {
	return InvalidatePostDetailCacheByIDWithRedis(global.RedisDB, id)
}

func loadPostDetailJSONCacheWithContext[T any](ctx context.Context, key string, loader func() (T, error)) (T, error) {
	client := global.RedisDB
	if client == nil {
		return loader()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id, err := postDetailIDFromCacheKey(key)
	if err != nil {
		var zero T
		return zero, err
	}
	return loadJSONCacheWithFenceStoreContext(
		ctx,
		key,
		postCacheTTL,
		func(key string) (string, error) {
			return client.WithContext(ctx).Get(key).Result()
		},
		func() (string, error) {
			return getOrCreatePostDetailFenceToken(ctx, client, id)
		},
		func(token string, payload []byte, expiration time.Duration) (bool, error) {
			return setPostDetailCacheIfFenceMatches(ctx, client, id, token, payload, expiration)
		},
		loader,
	)
}

type postDetailFenceGetter func() (string, error)

type postDetailFencedSetter func(token string, payload []byte, expiration time.Duration) (bool, error)

// loadJSONCacheWithFenceStoreContext protects Post detail backfills while
// retaining the existing cache-hit and Singleflight behavior. A fence change
// triggers at most one fresh load; a second concurrent invalidation returns
// that result without caching it.
func loadJSONCacheWithFenceStoreContext[T any](
	ctx context.Context,
	key string,
	expiration time.Duration,
	getter cacheGetter,
	fenceGetter postDetailFenceGetter,
	setter postDetailFencedSetter,
	loader func() (T, error),
) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	cachedData, err := getter(key)
	switch {
	case err == nil:
		return unmarshalCachedValue[T](cachedData)
	case err != redis.Nil:
		return zero, err
	}

	resultCh := postCacheGroup.DoChan(key, func() (interface{}, error) {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		cachedData, err := getter(key)
		switch {
		case err == nil:
			return unmarshalCachedValue[T](cachedData)
		case err != redis.Nil:
			return zero, err
		}

		for attempt := 0; attempt < 2; attempt++ {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			token, err := fenceGetter()
			if err != nil {
				return zero, err
			}
			value, err := loader()
			if err != nil {
				return zero, err
			}
			payload, err := json.Marshal(value)
			if err != nil {
				return zero, err
			}
			written, err := setter(token, payload, expiration)
			if err != nil {
				return zero, err
			}
			if written {
				return value, nil
			}

			// A newer request may already have populated the cache after the
			// invalidation. Prefer it before spending another database read.
			cachedData, err = getter(key)
			switch {
			case err == nil:
				return unmarshalCachedValue[T](cachedData)
			case err != redis.Nil:
				return zero, err
			}
			if attempt == 1 {
				return value, nil
			}
		}
		return zero, fmt.Errorf("Post detail cache load exhausted unexpectedly for key %s", key)
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return zero, result.Err
		}
		value, ok := result.Val.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected cache result type for key %s", key)
		}
		return value, nil
	}
}

// loadJSONCacheWithStore 核心缓存加载逻辑，集成了 Singleflight 防击穿机制
func loadJSONCacheWithStore[T any](
	key string,
	expiration time.Duration,
	getter cacheGetter,
	setter cacheSetter,
	loader func() (T, error),
) (T, error) {
	return loadJSONCacheWithStoreContext(context.Background(), key, expiration, getter, setter, loader)
}

func loadJSONCacheWithStoreContext[T any](
	ctx context.Context,
	key string,
	expiration time.Duration,
	getter cacheGetter,
	setter cacheSetter,
	loader func() (T, error),
) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

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
	resultCh := postCacheGroup.DoChan(key, func() (interface{}, error) {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
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
	var value interface{}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return zero, result.Err
		}
		value = result.Val
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
