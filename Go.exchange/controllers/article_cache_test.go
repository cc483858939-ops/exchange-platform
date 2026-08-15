package controllers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
func TestLoadJSONCacheWithStorePreservesArticleAuthorDTO(t *testing.T) {
	articleCacheGroup = singleflight.Group{}
	cache := map[string]string{}
	loads := 0
	getter := func(key string) (string, error) {
		value, ok := cache[key]
		if !ok {
			return "", redis.Nil
		}
		return value, nil
	}
	setter := func(key string, payload []byte, _ time.Duration) error {
		cache[key] = string(payload)
		return nil
	}
	loader := func() (articleResponse, error) {
		loads++
		return articleResponse{
			ID:           42,
			Title:        "cached article",
			LikeCount:    11,
			CommentCount: 3,
			Author:       publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Alice Chen", AvatarURL: "/api/files/profile-avatars/7/avatar.jpg"},
		}, nil
	}

	miss, err := loadJSONCacheWithStore("article:detail:v3:42", time.Minute, getter, setter, loader)
	if err != nil {
		t.Fatal(err)
	}
	hit, err := loadJSONCacheWithStore("article:detail:v3:42", time.Minute, getter, setter, loader)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("loader calls=%d want 1", loads)
	}
	if miss.Author != hit.Author || hit.Author.ID != 7 || hit.Author.Username != "alice" || hit.Author.DisplayName != "Alice Chen" || hit.Author.AvatarURL != "/api/files/profile-avatars/7/avatar.jpg" || miss.LikeCount != 11 || hit.LikeCount != 11 || miss.CommentCount != 3 || hit.CommentCount != 3 {
		t.Fatalf("author was not preserved across cache hit: miss=%+v hit=%+v", miss.Author, hit.Author)
	}
}

func TestHydrateArticleResponseAuthorsDeduplicatesAndPreservesArticleFields(t *testing.T) {
	originalLoader := loadPublicAuthorsByIDs
	t.Cleanup(func() { loadPublicAuthorsByIDs = originalLoader })
	var requested []uint
	loadPublicAuthorsByIDs = func(ids []uint) (map[uint]publicAuthorResponse, error) {
		requested = append([]uint(nil), ids...)
		return map[uint]publicAuthorResponse{
			7: {ID: 7, Username: "alice", DisplayName: "Alice Chen", AvatarURL: "new.jpg"},
			8: {ID: 8, Username: "bob", DisplayName: "Bob", AvatarURL: "bob.jpg"},
		}, nil
	}

	responses := []articleResponse{
		{ID: 101, Title: "first", Content: "one", LikeCount: 4, CommentCount: 2, Author: publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Old", AvatarURL: "old.jpg"}},
		{ID: 102, Title: "second", Content: "two", LikeCount: 5, CommentCount: 3, Author: publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Old", AvatarURL: "old.jpg"}},
		{ID: 103, Title: "third", Content: "three", LikeCount: 6, CommentCount: 4, Author: publicAuthorResponse{ID: 8, Username: "bob", DisplayName: "Old Bob", AvatarURL: "old-bob.jpg"}},
	}

	if err := hydrateArticleResponseAuthors(responses); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != 7 || requested[1] != 8 {
		t.Fatalf("hydration should deduplicate IDs for the bulk loader: %v", requested)
	}
	if responses[0].Author.DisplayName != "Alice Chen" || responses[0].Author.AvatarURL != "new.jpg" || responses[1].Author != responses[0].Author {
		t.Fatalf("current author identity was not applied: %#v", responses)
	}
	if responses[0].ID != 101 || responses[0].Title != "first" || responses[0].Content != "one" || responses[0].LikeCount != 4 || responses[0].CommentCount != 2 || responses[2].ID != 103 {
		t.Fatalf("non-author article fields changed: %#v", responses)
	}
}

func TestHydrateArticleResponseAuthorsDeduplicatesLoaderInput(t *testing.T) {
	originalLoader := loadPublicAuthorsByIDs
	t.Cleanup(func() { loadPublicAuthorsByIDs = originalLoader })
	var requested []uint
	loadPublicAuthorsByIDs = func(ids []uint) (map[uint]publicAuthorResponse, error) {
		requested = append([]uint(nil), ids...)
		return map[uint]publicAuthorResponse{
			7: {ID: 7, Username: "alice"},
			8: {ID: 8, Username: "bob"},
		}, nil
	}
	responses := []articleResponse{
		{Author: publicAuthorResponse{ID: 7}},
		{Author: publicAuthorResponse{ID: 7}},
		{Author: publicAuthorResponse{ID: 8}},
	}
	if err := hydrateArticleResponseAuthors(responses); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != 7 || requested[1] != 8 {
		t.Fatalf("unexpected hydration IDs: %v", requested)
	}
}

func TestHydrateArticleResponseAuthorsRejectsMissingAuthor(t *testing.T) {
	originalLoader := loadPublicAuthorsByIDs
	t.Cleanup(func() { loadPublicAuthorsByIDs = originalLoader })
	loadPublicAuthorsByIDs = func([]uint) (map[uint]publicAuthorResponse, error) {
		return map[uint]publicAuthorResponse{}, nil
	}
	responses := []articleResponse{{Author: publicAuthorResponse{ID: 7, Username: "stale"}}}
	if err := hydrateArticleResponseAuthors(responses); err == nil {
		t.Fatal("expected missing author hydration to fail")
	}
	if responses[0].Author.Username != "stale" {
		t.Fatalf("missing author should not silently use a placeholder: %#v", responses[0].Author)
	}
}

func TestLoadArticleDetailCacheHitReturnsCachedAuthorWithoutDatabaseOrHydration(t *testing.T) {
	originalCacheLoader := loadArticleDetailCache
	originalAuthorLoader := loadPublicAuthorsByIDs
	originalDB := global.Db
	t.Cleanup(func() {
		loadArticleDetailCache = originalCacheLoader
		loadPublicAuthorsByIDs = originalAuthorLoader
		global.Db = originalDB
	})

	global.Db = nil
	loadPublicAuthorsByIDs = func([]uint) (map[uint]publicAuthorResponse, error) {
		t.Fatal("cache hit must not hydrate article authors")
		return nil, nil
	}
	cached := articleResponse{
		ID:     123,
		Title:  "cached article",
		Author: publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Cached Alice", AvatarURL: "cached.jpg"},
	}
	loadArticleDetailCache = func(key string, loader func() (articleResponse, error)) (articleResponse, error) {
		if key != articleDetailCacheKey("123") {
			t.Fatalf("unexpected article detail cache key: %q", key)
		}
		payload, err := json.Marshal(cached)
		if err != nil {
			t.Fatal(err)
		}
		return loadJSONCacheWithStore(
			key,
			articleCacheTTL,
			func(string) (string, error) { return string(payload), nil },
			func(string, []byte, time.Duration) error { return nil },
			loader,
		)
	}

	returned, err := loadArticleDetail("123")
	if err != nil {
		t.Fatal(err)
	}
	if returned.Author != cached.Author {
		t.Fatalf("cached author changed: got=%+v want=%+v", returned.Author, cached.Author)
	}
}

func TestLoadArticleDetailCacheMissLoadsAndCachesAuthorSummaryIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	fixture.Author.DisplayName = "Database Alice"
	fixture.Author.AvatarURL = "database.jpg"
	if err := db.Model(&models.User{}).Where("id = ?", fixture.Author.ID).Updates(map[string]any{
		"display_name": fixture.Author.DisplayName,
		"avatar_url":   fixture.Author.AvatarURL,
	}).Error; err != nil {
		t.Fatal(err)
	}

	queryLogger := &articleDetailSQLLogger{Interface: logger.Default}
	global.Db = db.Session(&gorm.Session{Logger: queryLogger})
	originalCacheLoader := loadArticleDetailCache
	t.Cleanup(func() { loadArticleDetailCache = originalCacheLoader })

	articleCacheGroup = singleflight.Group{}
	cache := map[string]string{}
	var writes int
	loadArticleDetailCache = func(key string, loader func() (articleResponse, error)) (articleResponse, error) {
		return loadJSONCacheWithStore(
			key,
			articleCacheTTL,
			func(key string) (string, error) {
				value, ok := cache[key]
				if !ok {
					return "", redis.Nil
				}
				return value, nil
			},
			func(key string, payload []byte, _ time.Duration) error {
				writes++
				cache[key] = string(payload)
				return nil
			},
			loader,
		)
	}

	id := strconv.FormatUint(uint64(fixture.Article.ID), 10)
	first, err := loadArticleDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadArticleDetail(id)
	if err != nil {
		t.Fatal(err)
	}

	wantAuthor := publicAuthorResponse{
		ID:          fixture.Author.ID,
		Username:    fixture.Author.Username,
		DisplayName: fixture.Author.DisplayName,
		AvatarURL:   fixture.Author.AvatarURL,
	}
	if first.Author != wantAuthor || second.Author != wantAuthor {
		t.Fatalf("author summary was not preserved: first=%+v second=%+v want=%+v", first.Author, second.Author, wantAuthor)
	}
	if second.ID != first.ID || second.Title != first.Title || second.Content != first.Content {
		t.Fatalf("cache hit changed the article response: first=%+v second=%+v", first, second)
	}
	if writes != 1 {
		t.Fatalf("cache writes=%d want 1", writes)
	}

	cacheKey := articleDetailCacheKey(id)
	payload, ok := cache[cacheKey]
	if !ok {
		t.Fatalf("cache key %q was not written", cacheKey)
	}
	expectedPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if payload != string(expectedPayload) {
		t.Fatalf("cache payload was not the complete article response: got=%s want=%s", payload, expectedPayload)
	}
	var cachedResponse articleResponse
	if err := json.Unmarshal([]byte(payload), &cachedResponse); err != nil {
		t.Fatal(err)
	}
	if cachedResponse.Author != wantAuthor {
		t.Fatalf("cached AuthorSummary=%+v want=%+v", cachedResponse.Author, wantAuthor)
	}

	queries := queryLogger.snapshot()
	articleQueries := 0
	userQueries := 0
	selectedAuthorQuery := false
	for _, query := range queries {
		normalized := strings.Join(strings.Fields(strings.ToLower(query)), "")
		normalized = strings.ReplaceAll(normalized, string(rune(34)), "")
		if strings.Contains(normalized, "fromarticles") {
			articleQueries++
		}
		if strings.Contains(normalized, "fromusers") {
			userQueries++
			selectedAuthorQuery = selectedAuthorQuery || strings.Contains(normalized, "selectid,username,display_name,avatar_urlfromusers")
		}
	}
	if articleQueries != 1 || userQueries != 1 {
		t.Fatalf("expected one article query and one author preload query, got articles=%d users=%d queries=%v", articleQueries, userQueries, queries)
	}
	if !selectedAuthorQuery {
		t.Fatalf("author preload selected more than the public summary fields: %v", queries)
	}
}

type articleDetailSQLLogger struct {
	logger.Interface
	mu      sync.Mutex
	queries []string
}

func (l *articleDetailSQLLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	if fc == nil {
		return
	}
	query, _ := fc()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = append(l.queries, query)
}

func (l *articleDetailSQLLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.queries...)
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
