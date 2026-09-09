package translation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

func (c *RedisCache) Get(key string) (string, error) {
	if c == nil || c.client == nil {
		return "", ErrCacheUnavailable
	}
	return c.client.Get(key).Result()
}

func (c *RedisCache) Set(key, value string, expiration time.Duration) error {
	if c == nil || c.client == nil {
		return ErrCacheUnavailable
	}
	return c.client.Set(key, value, expiration).Err()
}

func ContentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func IdentityHash(identity string) string {
	return ContentHash(strings.TrimSpace(identity))
}

func BackendIdentity(baseURL, model string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "\n" + strings.TrimSpace(model)
}

func CacheKey(postID uint, content, sourceLanguage, targetLanguage, backendIdentity, promptVersion string) string {
	return fmt.Sprintf(
		"translation:v1:%d:%s:%s:%s:%s:%s",
		postID,
		ContentHash(content),
		cacheComponent(sourceLanguage),
		cacheComponent(targetLanguage),
		IdentityHash(backendIdentity),
		IdentityHash(promptVersion),
	)
}

func cacheComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "default"
	}
	return builder.String()
}

func isRedisCacheMiss(err error) bool {
	return errors.Is(err, redis.Nil)
}
