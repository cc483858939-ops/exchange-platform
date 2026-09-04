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
	postCacheGroup = singleflight.Group{}

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

			payload, err := loadJSONCacheWithStore("post:detail:42", time.Minute, getter, setter, loader)
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
	postCacheGroup = singleflight.Group{}

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
	keys := []string{"post:detail:7", "post:detail:8"}
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
func TestLoadJSONCacheWithStorePreservesPostAuthorDTO(t *testing.T) {
	postCacheGroup = singleflight.Group{}
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
	loader := func() (postResponse, error) {
		loads++
		return postResponse{
			ID:         42,
			Visibility: "public",
			LikeCount:  11,
			ReplyCount: 3,
			Author:     publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Alice Chen", AvatarURL: "/api/files/profile-avatars/7/avatar.jpg"},
		}, nil
	}

	miss, err := loadJSONCacheWithStore("post:detail:v3:42", time.Minute, getter, setter, loader)
	if err != nil {
		t.Fatal(err)
	}
	hit, err := loadJSONCacheWithStore("post:detail:v3:42", time.Minute, getter, setter, loader)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("loader calls=%d want 1", loads)
	}
	if miss.Author != hit.Author || hit.Author.ID != 7 || hit.Author.Username != "alice" || hit.Author.DisplayName != "Alice Chen" || hit.Author.AvatarURL != "/api/files/profile-avatars/7/avatar.jpg" || miss.LikeCount != 11 || hit.LikeCount != 11 || miss.ReplyCount != 3 || hit.ReplyCount != 3 {
		t.Fatalf("author was not preserved across cache hit: miss=%+v hit=%+v", miss.Author, hit.Author)
	}
}

func TestHydratePostResponseAuthorsDeduplicatesAndPreservesPostFields(t *testing.T) {
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

	responses := []postResponse{
		{ID: 101, Content: "one", LikeCount: 4, ReplyCount: 2, Author: publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Old", AvatarURL: "old.jpg"}},
		{ID: 102, Content: "two", LikeCount: 5, ReplyCount: 3, Author: publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Old", AvatarURL: "old.jpg"}},
		{ID: 103, Content: "three", LikeCount: 6, ReplyCount: 4, Author: publicAuthorResponse{ID: 8, Username: "bob", DisplayName: "Old Bob", AvatarURL: "old-bob.jpg"}},
	}

	if err := hydratePostResponseAuthors(responses); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != 7 || requested[1] != 8 {
		t.Fatalf("hydration should deduplicate IDs for the bulk loader: %v", requested)
	}
	if responses[0].Author.DisplayName != "Alice Chen" || responses[0].Author.AvatarURL != "new.jpg" || responses[1].Author != responses[0].Author {
		t.Fatalf("current author identity was not applied: %#v", responses)
	}
	if responses[0].ID != 101 || responses[0].Content != "one" || responses[0].LikeCount != 4 || responses[0].ReplyCount != 2 || responses[2].ID != 103 {
		t.Fatalf("post fields changed: %#v", responses)
	}
}

func TestHydratePostResponseAuthorsDeduplicatesLoaderInput(t *testing.T) {
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
	responses := []postResponse{
		{Author: publicAuthorResponse{ID: 7}},
		{Author: publicAuthorResponse{ID: 7}},
		{Author: publicAuthorResponse{ID: 8}},
	}
	if err := hydratePostResponseAuthors(responses); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != 7 || requested[1] != 8 {
		t.Fatalf("unexpected hydration IDs: %v", requested)
	}
}

func TestHydratePostResponseAuthorsRejectsMissingAuthor(t *testing.T) {
	originalLoader := loadPublicAuthorsByIDs
	t.Cleanup(func() { loadPublicAuthorsByIDs = originalLoader })
	loadPublicAuthorsByIDs = func([]uint) (map[uint]publicAuthorResponse, error) {
		return map[uint]publicAuthorResponse{}, nil
	}
	responses := []postResponse{{Author: publicAuthorResponse{ID: 7, Username: "stale"}}}
	if err := hydratePostResponseAuthors(responses); err == nil {
		t.Fatal("expected missing author hydration to fail")
	}
	if responses[0].Author.Username != "stale" {
		t.Fatalf("missing author should not silently use a placeholder: %#v", responses[0].Author)
	}
}

func TestLoadPostDetailCacheHitReturnsCachedAuthorWithoutDatabaseOrHydration(t *testing.T) {
	originalCacheLoader := loadPostDetailCache
	originalAuthorLoader := loadPublicAuthorsByIDs
	originalDB := global.Db
	t.Cleanup(func() {
		loadPostDetailCache = originalCacheLoader
		loadPublicAuthorsByIDs = originalAuthorLoader
		global.Db = originalDB
	})

	global.Db = nil
	loadPublicAuthorsByIDs = func([]uint) (map[uint]publicAuthorResponse, error) {
		t.Fatal("cache hit must not hydrate post authors")
		return nil, nil
	}
	now := time.Now().UTC()
	cached := postResponse{
		PublishedAt: &now,
		ID:          123,
		Visibility:  "public",
		Author:      publicAuthorResponse{ID: 7, Username: "alice", DisplayName: "Cached Alice", AvatarURL: "cached.jpg"},
	}
	loadPostDetailCache = func(key string, loader func() (postResponse, error)) (postResponse, error) {
		if key != postDetailCacheKey("123") {
			t.Fatalf("unexpected post detail cache key: %q", key)
		}
		payload, err := json.Marshal(cached)
		if err != nil {
			t.Fatal(err)
		}
		return loadJSONCacheWithStore(
			key,
			postCacheTTL,
			func(string) (string, error) { return string(payload), nil },
			func(string, []byte, time.Duration) error { return nil },
			loader,
		)
	}

	returned, err := loadPostDetail("123")
	if err != nil {
		t.Fatal(err)
	}
	if returned.Author != cached.Author {
		t.Fatalf("cached author changed: got=%+v want=%+v", returned.Author, cached.Author)
	}
}

func TestLoadPostDetailCacheMissLoadsAndCachesAuthorSummaryIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	fixture.Author.DisplayName = "Database Alice"
	fixture.Author.AvatarURL = "database.jpg"
	if err := db.Model(&models.User{}).Where("id = ?", fixture.Author.ID).Updates(map[string]any{
		"display_name": fixture.Author.DisplayName,
		"avatar_url":   fixture.Author.AvatarURL,
	}).Error; err != nil {
		t.Fatal(err)
	}

	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	global.Db = db.Session(&gorm.Session{Logger: queryLogger})
	originalCacheLoader := loadPostDetailCache
	t.Cleanup(func() { loadPostDetailCache = originalCacheLoader })

	postCacheGroup = singleflight.Group{}
	cache := map[string]string{}
	var writes int
	loadPostDetailCache = func(key string, loader func() (postResponse, error)) (postResponse, error) {
		return loadJSONCacheWithStore(
			key,
			postCacheTTL,
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
	first, err := loadPostDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadPostDetail(id)
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
	if second.ID != first.ID || second.Content != first.Content {
		t.Fatalf("cache hit changed the post response: first=%+v second=%+v", first, second)
	}
	if writes != 1 {
		t.Fatalf("cache writes=%d want 1", writes)
	}

	cacheKey := postDetailCacheKey(id)
	payload, ok := cache[cacheKey]
	if !ok {
		t.Fatalf("cache key %q was not written", cacheKey)
	}
	expectedPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if payload != string(expectedPayload) {
		t.Fatalf("cache payload was not the complete post response: got=%s want=%s", payload, expectedPayload)
	}
	var cachedResponse postResponse
	if err := json.Unmarshal([]byte(payload), &cachedResponse); err != nil {
		t.Fatal(err)
	}
	if cachedResponse.Author != wantAuthor {
		t.Fatalf("cached AuthorSummary=%+v want=%+v", cachedResponse.Author, wantAuthor)
	}

	queries := queryLogger.snapshot()
	postQueries := 0
	userQueries := 0
	mediaQueries := 0
	selectedAuthorQuery := false
	for _, query := range queries {
		normalized := strings.Join(strings.Fields(strings.ToLower(query)), "")
		normalized = strings.ReplaceAll(normalized, string(rune(34)), "")
		if strings.Contains(normalized, "fromposts") {
			postQueries++
		}
		if strings.Contains(normalized, "selectid,username,display_name,avatar_urlfromusers") {
			userQueries++
			selectedAuthorQuery = true
		}
		if strings.Contains(normalized, "frompost_media") {
			mediaQueries++
		}
	}
	if postQueries != 1 || userQueries != 1 || mediaQueries != 1 {
		t.Fatalf("expected one post query, one author preload query, and one media query, got posts=%d users=%d media=%d queries=%v", postQueries, userQueries, mediaQueries, queries)
	}
	if !selectedAuthorQuery {
		t.Fatalf("author preload selected more than the public summary fields: %v", queries)
	}
}

func TestIsPublicPostResponseAt(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	cases := []struct {
		name       string
		published  *time.Time
		visibility string
		deleted    bool
		want       bool
	}{
		{name: "public post", published: &past, visibility: "public", want: true},
		{name: "nil published at", visibility: "public", want: false},
		{name: "future created post", published: &future, visibility: "public", want: true},
		{name: "private post", published: &past, visibility: "private", want: false},
		{name: "deleted post", published: &past, visibility: "public", deleted: true, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := postResponse{
				PublishedAt: testCase.published,
				Visibility:  testCase.visibility,
				Deleted:     testCase.deleted,
			}
			if got := isPublicPostResponseAt(response, now); got != testCase.want {
				t.Fatalf("public=%v want=%v response=%#v", got, testCase.want, response)
			}
		})
	}
}

func TestLoadPostDetailRejectsInvalidCachedResponseAndBestEffortDeletes(t *testing.T) {
	originalCacheLoader := loadPostDetailCache
	originalInvalidator := invalidatePostDetailCacheKey
	originalDB := global.Db
	t.Cleanup(func() {
		loadPostDetailCache = originalCacheLoader
		invalidatePostDetailCacheKey = originalInvalidator
		global.Db = originalDB
	})

	global.Db = nil
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	loadPostDetailCache = func(string, func() (postResponse, error)) (postResponse, error) {
		return postResponse{
			ID:          42,
			PublishedAt: &future,
			Visibility:  "private",
			Author:      publicAuthorResponse{ID: 7, Username: "cached"},
		}, nil
	}
	var deletedKey string
	invalidatePostDetailCacheKey = func(key string) error {
		deletedKey = key
		return gorm.ErrInvalidData
	}

	_, err := loadPostDetail("42")
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("invalid cached response error=%v want=%v", err, gorm.ErrRecordNotFound)
	}
	if deletedKey != postDetailCacheKey("42") {
		t.Fatalf("deleted key=%q", deletedKey)
	}
}

func TestLoadPostDetailMissFiltersDeletedPostsIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	posts := []models.Post{
		{
			AuthorID: fixture.Author.ID, Content: "detail-valid", Visibility: "public",
		},
		{
			AuthorID: fixture.Author.ID, Content: "detail-future", Visibility: "public",
		},
		{
			AuthorID: fixture.Author.ID, Content: "detail-short", Visibility: "public",
		},
		{
			AuthorID: fixture.Author.ID, Content: "detail-expired", Visibility: "public",
		},
		{
			AuthorID: fixture.Author.ID, Content: "detail-deleted", Visibility: "public",
		},
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(posts))
		for _, post := range posts {
			if post.ID != 0 {
				ids = append(ids, post.ID)
			}
		}
		if len(ids) > 0 {
			db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
		}
	})
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&posts[len(posts)-1]).Error; err != nil {
		t.Fatal(err)
	}

	originalCacheLoader := loadPostDetailCache
	originalDB := global.Db
	t.Cleanup(func() {
		loadPostDetailCache = originalCacheLoader
		global.Db = originalDB
	})
	global.Db = db
	loadPostDetailCache = func(_ string, loader func() (postResponse, error)) (postResponse, error) {
		return loader()
	}

	validID := strconv.FormatUint(uint64(posts[0].ID), 10)
	response, err := loadPostDetail(validID)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != posts[0].ID || response.Author.ID != fixture.Author.ID {
		t.Fatalf("valid detail response=%#v", response)
	}
	for _, index := range []int{1, 2, 3} {
		response, err := loadPostDetail(strconv.FormatUint(uint64(posts[index].ID), 10))
		if err != nil || response.ID != posts[index].ID {
			t.Fatalf("active post %d error=%v response=%#v", posts[index].ID, err, response)
		}
	}
	{
		index := 4
		_, err := loadPostDetail(strconv.FormatUint(uint64(posts[index].ID), 10))
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("ineligible post %d error=%v want=%v", posts[index].ID, err, gorm.ErrRecordNotFound)
		}
	}
}

type postDetailSQLLogger struct {
	logger.Interface
	mu      sync.Mutex
	queries []string
}

func (l *postDetailSQLLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	if fc == nil {
		return
	}
	query, _ := fc()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = append(l.queries, query)
}

func (l *postDetailSQLLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.queries...)
}

// TestLoadJSONCacheWithStoreReturnsCachedValueWithoutReloading 测试缓存命中场景：
// 如果缓存中已经有数据，则直接返回，不再触发回源逻辑。
func TestLoadJSONCacheWithStoreReturnsCachedValueWithoutReloading(t *testing.T) {
	postCacheGroup = singleflight.Group{}

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

	payload, err := loadJSONCacheWithStore("posts", time.Minute, getter, setter, loader)
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
