package controllers

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/global"

	"github.com/go-redis/redis/v7"
	"golang.org/x/sync/singleflight"
)

type postCacheCancellationStore struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *postCacheCancellationStore) get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (s *postCacheCancellationStore) set(ctx context.Context, key string, payload []byte, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = string(payload)
	return nil
}

type postCacheWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func newPostCacheWaitContext(parent context.Context) (*postCacheWaitContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	return &postCacheWaitContext{Context: ctx, waiting: make(chan struct{})}, cancel
}

func (c *postCacheWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func waitPostCacheSignal(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func drainPostCacheFlight(t *testing.T, key string) {
	t.Helper()
	result := postCacheGroup.DoChan(key, func() (interface{}, error) { return nil, nil })
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out draining Singleflight key %q", key)
	}
}

type postCacheTestResult struct {
	value cacheTestPayload
	err   error
}

func loadPostCacheTestValue(ctx context.Context, key string, store *postCacheCancellationStore, loader func(context.Context) (cacheTestPayload, error)) (cacheTestPayload, error) {
	return loadJSONCacheWithStoreContext(ctx, key, time.Minute, store.get, store.set, loader)
}

func TestPostCacheSharedFlightIsolatedFromEitherWaiterCancellation(t *testing.T) {
	for _, cancelWaiter := range []string{"first", "second"} {
		t.Run(cancelWaiter, func(t *testing.T) {
			postCacheGroup = singleflight.Group{}
			store := &postCacheCancellationStore{values: make(map[string]string)}
			loaderStarted := make(chan context.Context, 2)
			releaseLoader := make(chan struct{})
			var releaseOnce sync.Once
			var loads atomic.Int32
			loader := func(workCtx context.Context) (cacheTestPayload, error) {
				loads.Add(1)
				loaderStarted <- workCtx
				select {
				case <-releaseLoader:
					return cacheTestPayload{Value: "shared"}, nil
				case <-workCtx.Done():
					return cacheTestPayload{}, workCtx.Err()
				}
			}

			ctxA, cancelA := newPostCacheWaitContext(context.Background())
			ctxB, cancelB := newPostCacheWaitContext(context.Background())
			defer cancelA()
			defer cancelB()
			resultA := make(chan postCacheTestResult, 1)
			resultB := make(chan postCacheTestResult, 1)
			go func() {
				value, err := loadPostCacheTestValue(ctxA, "post:detail:a15-cancel-"+cancelWaiter, store, loader)
				resultA <- postCacheTestResult{value: value, err: err}
			}()
			workCtx := <-loaderStarted
			go func() {
				value, err := loadPostCacheTestValue(ctxB, "post:detail:a15-cancel-"+cancelWaiter, store, loader)
				resultB <- postCacheTestResult{value: value, err: err}
			}()
			waitPostCacheSignal(t, ctxB.waiting, "the second waiter to join Singleflight")

			if cancelWaiter == "first" {
				cancelA()
				select {
				case result := <-resultA:
					if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("first waiter error=%v, want context.Canceled", result.err)
					}
				case <-time.After(time.Second):
					t.Fatal("first waiter did not return promptly after cancellation")
				}
			} else {
				cancelB()
				select {
				case result := <-resultB:
					if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("second waiter error=%v, want context.Canceled", result.err)
					}
				case <-time.After(time.Second):
					t.Fatal("second waiter did not return promptly after cancellation")
				}
			}

			if err := workCtx.Err(); err != nil {
				t.Fatalf("one waiter cancellation canceled shared work: %v", err)
			}
			releaseOnce.Do(func() { close(releaseLoader) })
			var surviving postCacheTestResult
			if cancelWaiter == "first" {
				surviving = <-resultB
			} else {
				surviving = <-resultA
			}
			if surviving.err != nil || surviving.value.Value != "shared" {
				t.Fatalf("surviving waiter result=%+v, want shared success", surviving)
			}
			if loads.Load() != 1 {
				t.Fatalf("loader calls=%d, want 1", loads.Load())
			}
			releaseOnce.Do(func() { close(releaseLoader) })
		})
	}
}

func TestPostCacheThreeWaitersShareOneLoad(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	store := &postCacheCancellationStore{values: make(map[string]string)}
	loaderStarted := make(chan context.Context, 3)
	releaseLoader := make(chan struct{})
	var loads atomic.Int32
	loader := func(workCtx context.Context) (cacheTestPayload, error) {
		loads.Add(1)
		loaderStarted <- workCtx
		select {
		case <-releaseLoader:
			return cacheTestPayload{Value: "three"}, nil
		case <-workCtx.Done():
			return cacheTestPayload{}, workCtx.Err()
		}
	}
	ctxA, cancelA := newPostCacheWaitContext(context.Background())
	ctxB, cancelB := newPostCacheWaitContext(context.Background())
	ctxC, cancelC := newPostCacheWaitContext(context.Background())
	defer cancelA()
	defer cancelB()
	defer cancelC()
	results := make(chan postCacheTestResult, 3)
	for _, ctx := range []*postCacheWaitContext{ctxA, ctxB, ctxC} {
		ctx := ctx
		go func() {
			value, err := loadPostCacheTestValue(ctx, "post:detail:a15-three-waiters", store, loader)
			results <- postCacheTestResult{value: value, err: err}
		}()
		if ctx == ctxA {
			<-loaderStarted
		} else {
			waitPostCacheSignal(t, ctx.waiting, "a follower to join Singleflight")
		}
	}
	close(releaseLoader)
	for i := 0; i < 3; i++ {
		result := <-results
		if result.err != nil || result.value.Value != "three" {
			t.Fatalf("waiter result=%+v, want shared success", result)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls=%d, want 1", loads.Load())
	}
}

func TestPostCacheSharedFlightsForDifferentKeysAreIndependent(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	store := &postCacheCancellationStore{values: make(map[string]string)}
	started := make(chan struct {
		key string
		ctx context.Context
	}, 2)
	releases := map[string]chan struct{}{
		"post:detail:a15-key-a": make(chan struct{}),
		"post:detail:a15-key-b": make(chan struct{}),
	}
	var loads atomic.Int32
	loaderForKey := func(key string) func(context.Context) (cacheTestPayload, error) {
		return func(workCtx context.Context) (cacheTestPayload, error) {
			loads.Add(1)
			started <- struct {
				key string
				ctx context.Context
			}{key: key, ctx: workCtx}
			select {
			case <-releases[key]:
				return cacheTestPayload{Value: key}, nil
			case <-workCtx.Done():
				return cacheTestPayload{}, workCtx.Err()
			}
		}
	}
	results := make(chan postCacheTestResult, 2)
	for _, key := range []string{"post:detail:a15-key-a", "post:detail:a15-key-b"} {
		key := key
		go func() {
			value, err := loadPostCacheTestValue(context.Background(), key, store, loaderForKey(key))
			results <- postCacheTestResult{value: value, err: err}
		}()
	}
	first := <-started
	second := <-started
	if first.key == second.key || first.ctx == second.ctx {
		t.Fatalf("different keys shared one work context: first=%s second=%s", first.key, second.key)
	}
	close(releases[first.key])
	close(releases[second.key])
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil || result.value.Value == "" {
			t.Fatalf("key result=%+v, want independent success", result)
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("loader calls=%d, want one per key", loads.Load())
	}
}

func TestPostCacheAllWaitersCancelButSharedWorkUsesItsOwnDeadline(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "150ms")
	postCacheGroup = singleflight.Group{}
	const key = "post:detail:a15-all-cancel"
	store := &postCacheCancellationStore{values: make(map[string]string)}
	loaderStarted := make(chan context.Context, 2)
	loaderFinished := make(chan error, 2)
	var loads atomic.Int32
	loader := func(workCtx context.Context) (cacheTestPayload, error) {
		loads.Add(1)
		loaderStarted <- workCtx
		<-workCtx.Done()
		loaderFinished <- workCtx.Err()
		return cacheTestPayload{}, workCtx.Err()
	}
	ctxA, cancelA := newPostCacheWaitContext(context.Background())
	ctxB, cancelB := newPostCacheWaitContext(context.Background())
	defer cancelA()
	defer cancelB()
	results := make(chan postCacheTestResult, 2)
	for index, ctx := range []*postCacheWaitContext{ctxA, ctxB} {
		ctx := ctx
		go func() {
			value, err := loadPostCacheTestValue(ctx, key, store, loader)
			results <- postCacheTestResult{value: value, err: err}
		}()
		if index == 0 {
			workCtx := <-loaderStarted
			if _, ok := workCtx.Deadline(); !ok {
				t.Fatal("shared work context has no deadline")
			}
		} else {
			waitPostCacheSignal(t, ctx.waiting, "the second waiter to join before cancellation")
		}
	}
	cancelA()
	cancelB()
	for i := 0; i < 2; i++ {
		result := <-results
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("waiter error=%v, want context.Canceled", result.err)
		}
	}
	select {
	case err := <-loaderFinished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shared loader ended with %v, want its own DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shared loader did not stop at its configured deadline")
	}
	if loads.Load() != 1 {
		t.Fatalf("shared loader calls=%d, want 1", loads.Load())
	}
	drainPostCacheFlight(t, key)
}

func TestPostCacheActiveWaiterReceivesSharedDeadlineExceeded(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "100ms")
	postCacheGroup = singleflight.Group{}
	store := &postCacheCancellationStore{values: make(map[string]string)}
	loader := func(workCtx context.Context) (cacheTestPayload, error) {
		<-workCtx.Done()
		return cacheTestPayload{}, workCtx.Err()
	}
	result, err := loadPostCacheTestValue(context.Background(), "post:detail:a15-active-timeout", store, loader)
	if !errors.Is(err, context.DeadlineExceeded) || result.Value != "" {
		t.Fatalf("result=%+v error=%v, want shared DeadlineExceeded", result, err)
	}
}

func TestPostCacheSharedRedisAndFenceOperationsUseOneDetachedContext(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	requestCtxB, cancelRequestB := newPostCacheWaitContext(context.Background())
	defer cancelRequestB()
	secondGetStarted := make(chan context.Context, 1)
	releaseSecondGet := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSecondGet) })
	var getterCalls atomic.Int32
	var sharedGetterContexts, fenceContexts, loaderContexts, setterContexts []context.Context
	var contextsMu sync.Mutex
	getter := func(ctx context.Context, _ string) (string, error) {
		getterCalls.Add(1)
		if ctx == requestCtx || ctx == requestCtxB {
			return "", redis.Nil
		}
		contextsMu.Lock()
		sharedGetterContexts = append(sharedGetterContexts, ctx)
		call := len(sharedGetterContexts)
		contextsMu.Unlock()
		if call == 1 {
			secondGetStarted <- ctx
			<-releaseSecondGet
		}
		return "", redis.Nil
	}
	fenceGetter := func(ctx context.Context) (string, error) {
		contextsMu.Lock()
		fenceContexts = append(fenceContexts, ctx)
		contextsMu.Unlock()
		return "stable", nil
	}
	var setterCalls atomic.Int32
	setter := func(ctx context.Context, _ string, _ []byte, _ time.Duration) (bool, error) {
		contextsMu.Lock()
		setterContexts = append(setterContexts, ctx)
		contextsMu.Unlock()
		return setterCalls.Add(1) > 1, nil
	}
	loader := func(ctx context.Context) (cacheTestPayload, error) {
		contextsMu.Lock()
		loaderContexts = append(loaderContexts, ctx)
		contextsMu.Unlock()
		return cacheTestPayload{Value: "fresh"}, nil
	}
	type fencedResult struct {
		value cacheTestPayload
		err   error
	}
	resultA := make(chan fencedResult, 1)
	resultB := make(chan fencedResult, 1)
	go func() {
		value, err := loadJSONCacheWithFenceStoreContext(requestCtx, "post:detail:a15-context", postCacheTTL,
			getter, fenceGetter, setter, loader)
		resultA <- fencedResult{value: value, err: err}
	}()
	workCtx := <-secondGetStarted
	if workCtx == requestCtx {
		t.Fatal("second Redis GET reused the first request context")
	}
	if _, ok := workCtx.Deadline(); !ok {
		t.Fatal("shared work context has no deadline")
	}
	go func() {
		value, err := loadJSONCacheWithFenceStoreContext(requestCtxB, "post:detail:a15-context", postCacheTTL,
			getter, fenceGetter, setter, loader)
		resultB <- fencedResult{value: value, err: err}
	}()
	waitPostCacheSignal(t, requestCtxB.waiting, "the surviving waiter to join the shared task")
	cancelRequest()
	if result := <-resultA; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled request result=%+v, want context.Canceled", result)
	}
	if err := workCtx.Err(); err != nil {
		t.Fatalf("request cancellation canceled the shared Redis GET: %v", err)
	}
	releaseOnce.Do(func() { close(releaseSecondGet) })
	result := <-resultB
	if result.err != nil || result.value.Value != "fresh" {
		t.Fatalf("surviving waiter result=%+v, want fresh success", result)
	}

	contextsMu.Lock()
	defer contextsMu.Unlock()
	if getterCalls.Load() != 4 || len(sharedGetterContexts) != 2 || len(fenceContexts) != 2 || len(loaderContexts) != 2 || len(setterContexts) != 2 {
		t.Fatalf("calls: getters=%d shared_getters=%d fences=%d loaders=%d setters=%d", getterCalls.Load(), len(sharedGetterContexts), len(fenceContexts), len(loaderContexts), len(setterContexts))
	}
	for _, ctx := range sharedGetterContexts {
		if ctx != workCtx {
			t.Fatal("shared Redis GET did not receive the shared work context")
		}
	}
	for _, ctx := range fenceContexts {
		if ctx != workCtx {
			t.Fatal("fence getter did not receive the shared work context")
		}
	}
	for _, ctx := range loaderContexts {
		if ctx != workCtx {
			t.Fatal("loader did not receive the shared work context")
		}
	}
	for _, ctx := range setterContexts {
		if ctx != workCtx {
			t.Fatal("fenced setter did not receive the shared work context")
		}
	}
}

func TestLoadPostDetailGORMQueryUsesSharedWorkContext(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	originalDB, originalRedis, originalLoader := global.APIDb, global.RedisDB, loadPostDetailCache
	t.Cleanup(func() {
		global.APIDb, global.RedisDB, loadPostDetailCache = originalDB, originalRedis, originalLoader
	})
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	queryStarted := make(chan context.Context, 2)
	releaseQuery := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseQuery) })
	db := visibilityRefreshDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		queryStarted <- ctx
		<-releaseQuery
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("stop after observing the Post query context")
	})
	global.APIDb = db
	store := &postCacheCancellationStore{values: make(map[string]string)}
	loadPostDetailCache = func(ctx context.Context, key string, loader func(context.Context) (postResponse, error)) (postResponse, error) {
		return loadJSONCacheWithStoreContext(ctx, key, postCacheTTL, store.get, store.set, loader)
	}
	result := make(chan error, 1)
	key := postDetailCacheKey("42")
	go func() {
		_, err := loadPostDetail(requestCtx, "42")
		result <- err
	}()
	queryCtx := <-queryStarted
	if queryCtx == requestCtx {
		t.Fatal("GORM Post query reused the HTTP request context")
	}
	if _, ok := queryCtx.Deadline(); !ok {
		t.Fatal("GORM Post query context has no shared-work deadline")
	}
	cancelRequest()
	if err := queryCtx.Err(); err != nil {
		t.Fatalf("canceling the request canceled the in-flight GORM query: %v", err)
	}
	releaseOnce.Do(func() { close(releaseQuery) })
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request result error=%v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Post detail request did not return after cancellation")
	}
	drainPostCacheFlight(t, key)
}

func TestPostCacheNoRedisAndCacheHitKeepRequestContext(t *testing.T) {
	originalRedis := global.RedisDB
	global.RedisDB = nil
	t.Cleanup(func() { global.RedisDB = originalRedis })
	requestCtx := context.WithValue(context.Background(), requestContextTestKey{}, "a15")
	var received context.Context
	value, err := loadPostDetailJSONCacheWithContext(requestCtx, "post:detail:a15-no-redis", func(ctx context.Context) (cacheTestPayload, error) {
		received = ctx
		return cacheTestPayload{Value: "request"}, nil
	})
	if err != nil || value.Value != "request" || received != requestCtx {
		t.Fatalf("Redis-disabled load=%+v contextMatches=%t err=%v", value, received == requestCtx, err)
	}

	postCacheGroup = singleflight.Group{}
	reads, loaders, setters := 0, 0, 0
	getter := func(ctx context.Context, _ string) (string, error) {
		reads++
		if ctx != requestCtx {
			t.Fatal("cache-hit GET did not use the request context")
		}
		return `{"value":"cached"}`, nil
	}
	setter := func(context.Context, string, []byte, time.Duration) error { setters++; return nil }
	loader := func(context.Context) (cacheTestPayload, error) { loaders++; return cacheTestPayload{}, nil }
	value, err = loadJSONCacheWithStoreContext(requestCtx, "post:detail:a15-hit", time.Minute, getter, setter, loader)
	if err != nil || value.Value != "cached" || reads != 1 || loaders != 0 || setters != 0 {
		t.Fatalf("cache hit=%+v reads=%d loaders=%d setters=%d err=%v", value, reads, loaders, setters, err)
	}
}

type postCacheCancelOnResultContext struct {
	context.Context
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	checks int
}

func (c *postCacheCancelOnResultContext) Done() <-chan struct{} { return c.done }
func (c *postCacheCancelOnResultContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks++
	if c.checks == 1 {
		return nil
	}
	c.once.Do(func() { close(c.done) })
	return context.Canceled
}

func TestPostCacheResultIsSuppressedWhenRequestCancelsAtReceiveBoundary(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	ctx := &postCacheCancelOnResultContext{Context: context.Background(), done: make(chan struct{})}
	value, err := awaitPostCacheFlight(ctx, "post:detail:a15-result-cancel-race", func(context.Context) (cacheTestPayload, error) {
		return cacheTestPayload{Value: "must-not-be-used"}, nil
	})
	if !errors.Is(err, context.Canceled) || value.Value != "" {
		t.Fatalf("received result=%+v err=%v, want cancellation to suppress it", value, err)
	}
}

func TestPostCacheSharedContextExpiresAtConfiguredTimeout(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "100ms")
	postCacheGroup = singleflight.Group{}
	store := &postCacheCancellationStore{values: make(map[string]string)}
	sharedBudget := make(chan time.Duration, 1)
	loader := func(workCtx context.Context) (cacheTestPayload, error) {
		deadline, ok := workCtx.Deadline()
		if !ok {
			return cacheTestPayload{}, errors.New("shared context has no deadline")
		}
		sharedBudget <- time.Until(deadline)
		<-workCtx.Done()
		return cacheTestPayload{}, workCtx.Err()
	}
	_, err := loadPostCacheTestValue(context.Background(), "post:detail:a15-shared-timeout", store, loader)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared timeout error=%v, want context.DeadlineExceeded", err)
	}
	select {
	case budget := <-sharedBudget:
		if budget > 100*time.Millisecond || budget <= 0 {
			t.Fatalf("shared deadline budget=%s, want within configured 100ms", budget)
		}
	default:
		t.Fatal("loader did not observe its shared deadline")
	}
}

func TestPostCacheSharedTaskReturnsRealRedisOrDatabaseFailure(t *testing.T) {
	postCacheGroup = singleflight.Group{}
	requestErr := errors.New("shared cache failure")
	getterCalls := 0
	getter := func(context.Context, string) (string, error) {
		getterCalls++
		if getterCalls == 1 {
			return "", redis.Nil
		}
		return "", requestErr
	}
	setter := func(context.Context, string, []byte, time.Duration) error { return nil }
	loader := func(context.Context) (cacheTestPayload, error) {
		return cacheTestPayload{}, errors.New("must not load")
	}
	_, err := loadJSONCacheWithStoreContext(context.Background(), "post:detail:a15-real-error", time.Minute, getter, setter, loader)
	if !errors.Is(err, requestErr) {
		t.Fatalf("shared error=%v, want original Redis failure", err)
	}
}
