package controllers

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"golang.org/x/sync/singleflight"
)

// cacheTestPayload 定义测试用的数据结构
type cacheTestPayload struct {
	Value string `json:"value"`
}

// TestLoadJSONCacheWithStoreDeduplicatesConcurrentMisses 测试在缓存未命中时，
// 多个并发请求同一个 Key 是否能通过 Singleflight 机制确保只有一个请求执行回源加载逻辑。
func TestLoadJSONCacheWithStoreDeduplicatesConcurrentMisses(t *testing.T) {
	articleCacheGroup = singleflight.Group{}

	var loads atomic.Int32
	var mu sync.Mutex
	cache := map[string]string{}

	// getter 模拟从 Redis 获取数据（当前模拟未命中）
	getter := func(key string) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		value, ok := cache[key]
		if !ok {
			return "", redis.Nil // 返回 redis.Nil 表示缓存缺失
		}
		return value, nil
	}
	// setter 模拟将数据写入 Redis
	setter := func(key string, payload []byte, _ time.Duration) error {
		mu.Lock()
		defer mu.Unlock()

		cache[key] = string(payload)
		return nil
	}
	// loader 模拟回源逻辑（如查询数据库），并带有延迟以模拟耗时操作
	loader := func() (cacheTestPayload, error) {
		loads.Add(1) // 记录回源次数
		time.Sleep(20 * time.Millisecond)
		return cacheTestPayload{Value: "shared"}, nil
	}

	const workers = 100
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			payload, err := loadJSONCacheWithStore("article:detail:42", time.Minute, getter, setter, loader)
			if err != nil {
				errs <- err
				return
			}
			if payload.Value != "shared" {
				errs <- newUnexpectedPayloadError(payload.Value)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
	// 验证：尽管有 100 个并发请求，但 loader 应该只被调用了 1 次
	if loads.Load() != 1 {
		t.Fatalf("expected loader to run once, got %d", loads.Load())
	}
}

// TestLoadJSONCacheWithStoreSeparatesDifferentKeys 测试不同 Key 的请求是否能正确分离，
// 即每个 Key 都会触发各自的回源加载。
func TestLoadJSONCacheWithStoreSeparatesDifferentKeys(t *testing.T) {
	articleCacheGroup = singleflight.Group{}

	var loads atomic.Int32
	var mu sync.Mutex
	cache := map[string]string{}

	getter := func(key string) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		value, ok := cache[key]
		if !ok {
			return "", redis.Nil
		}
		return value, nil
	}
	setter := func(key string, payload []byte, _ time.Duration) error {
		mu.Lock()
		defer mu.Unlock()

		cache[key] = string(payload)
		return nil
	}

	loadWithKey := func(key string) func() (cacheTestPayload, error) {
		return func() (cacheTestPayload, error) {
			loads.Add(1)
			time.Sleep(20 * time.Millisecond)
			return cacheTestPayload{Value: key}, nil
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	keys := []string{"article:detail:7", "article:detail:8"}
	for _, key := range keys {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()

			payload, err := loadJSONCacheWithStore(key, time.Minute, getter, setter, loadWithKey(key))
			if err != nil {
				errs <- err
				return
			}
			if payload.Value != key {
				errs <- newUnexpectedPayloadError(payload.Value)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
	// 验证：两个不同的 Key，应该各自触发一次回源，共 2 次
	if loads.Load() != 2 {
		t.Fatalf("expected loader to run once per cache key, got %d", loads.Load())
	}
}

// TestLoadJSONCacheWithStoreReturnsCachedValueWithoutReloading 测试缓存命中场景：
// 如果缓存中已经有数据，则直接返回，不再触发回源逻辑。
func TestLoadJSONCacheWithStoreReturnsCachedValueWithoutReloading(t *testing.T) {
	articleCacheGroup = singleflight.Group{}

	var loads atomic.Int32
	getter := func(string) (string, error) {
		return `{"value":"cached"}`, nil
	}
	setter := func(string, []byte, time.Duration) error {
		return nil
	}
	loader := func() (cacheTestPayload, error) {
		loads.Add(1)
		return cacheTestPayload{Value: "db"}, nil
	}

	payload, err := loadJSONCacheWithStore("articles", time.Minute, getter, setter, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Value != "cached" {
		t.Fatalf("expected cached payload, got %q", payload.Value)
	}
	if loads.Load() != 0 {
		t.Fatalf("expected loader to be skipped, got %d calls", loads.Load())
	}
}

func newUnexpectedPayloadError(value string) error {
	return &unexpectedPayloadError{value: value}
}

type unexpectedPayloadError struct {
	value string
}

func (e *unexpectedPayloadError) Error() string {
	return "unexpected payload value: " + e.value
}
