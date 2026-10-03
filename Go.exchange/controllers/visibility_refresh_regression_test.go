package controllers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/auth"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type visibilityRefreshConnector struct {
	query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

func (c visibilityRefreshConnector) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c visibilityRefreshConnector) Driver() driver.Driver                        { return visibilityRefreshDriver{} }

type visibilityRefreshDriver struct{}

func (visibilityRefreshDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected open")
}
func (visibilityRefreshConnector) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (visibilityRefreshConnector) Close() error { return nil }
func (visibilityRefreshConnector) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c visibilityRefreshConnector) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, q, args)
}

type visibilityRefreshRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *visibilityRefreshRows) Columns() []string { return r.columns }
func (r *visibilityRefreshRows) Close() error      { return nil }
func (r *visibilityRefreshRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func visibilityRefreshDB(t *testing.T, query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)) *gorm.DB {
	t.Helper()
	connection := sql.OpenDB(visibilityRefreshConnector{query: query})
	t.Cleanup(func() { _ = connection.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

type visibilityRefreshCache struct {
	mu    sync.Mutex
	value string
}

func (c *visibilityRefreshCache) get(string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == "" {
		return "", redis.Nil
	}
	return c.value, nil
}
func (c *visibilityRefreshCache) set(_ string, payload []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = string(payload)
	return nil
}
func (c *visibilityRefreshCache) clear() { c.mu.Lock(); defer c.mu.Unlock(); c.value = "" }

func visibilityRefreshFixture(t *testing.T) (*visibilityRefreshCache, *atomic.Bool, postResponse) {
	t.Helper()
	originalDB, originalCache, originalInvalidator := global.APIDb, loadPostDetailCache, invalidatePostDetailCacheKey
	t.Cleanup(func() {
		global.APIDb, loadPostDetailCache, invalidatePostDetailCacheKey = originalDB, originalCache, originalInvalidator
	})
	visible := &atomic.Bool{}
	visible.Store(true)
	global.APIDb = visibilityRefreshDB(t, func(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.HasPrefix(q, "SELECT posts.id FROM") {
			for _, predicate := range []string{"posts.deleted_at IS NULL", "posts.visibility = 'public'", "post_author.deleted_at IS NULL"} {
				if !strings.Contains(q, predicate) {
					t.Errorf("missing public predicate %q: %s", predicate, q)
				}
			}
			rows := &visibilityRefreshRows{columns: []string{"id"}}
			if visible.Load() {
				rows.values = [][]driver.Value{{int64(42)}}
			}
			return rows, nil
		}
		if strings.Contains(q, "post_reposts") {
			return &visibilityRefreshRows{columns: []string{"post_id", "reposts"}}, nil
		}
		return nil, errors.New("unexpected detail query: " + q)
	})
	cache := &visibilityRefreshCache{}
	loadPostDetailCache = func(ctx context.Context, key string, loader func() (postResponse, error)) (postResponse, error) {
		return loadJSONCacheWithStoreContext(ctx, key, postCacheTTL, cache.get, cache.set, loader)
	}
	now := time.Now().UTC()
	cached := postResponse{ID: 42, PublishedAt: &now, Visibility: "public", Content: "private regression body", Author: publicAuthorResponse{ID: 7, Username: "alice"}}
	stubPostDeleteDependencies(t, nil)
	deletePostInTransaction = func(context.Context, uint, uint) (postDeleteResult, error) {
		visible.Store(false)
		return postDeleteResult{}, nil
	}
	return cache, visible, cached
}

func visibilityRefreshRead(t *testing.T) (int, string) {
	t.Helper()
	ctx, response := newPostDeleteContext("42", nil)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/posts/42", nil)
	GetPostByID(ctx)
	return response.Code, response.Body.String()
}

func TestDeletedPostWarmCacheCannotLeakAfterInvalidationFailure(t *testing.T) {
	cache, _, cached := visibilityRefreshFixture(t)
	payload, _ := json.Marshal(cached)
	_ = cache.set("", payload, postCacheTTL)
	if status, _ := visibilityRefreshRead(t); status != http.StatusOK {
		t.Fatalf("visible status=%d", status)
	}
	invalidatePostDeleteDetailCache = func(uint) error { return errors.New("DEL unavailable") }
	invalidatePostDetailCacheKey = func(string) error { return errors.New("DEL unavailable") }
	owner := uint(7)
	ctx, response := newPostDeleteContext("42", &owner)
	DeletePost(ctx)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d", response.Code)
	}
	for i := 0; i < 2; i++ {
		status, body := visibilityRefreshRead(t)
		if status != http.StatusNotFound || strings.Contains(body, cached.Content) {
			t.Fatalf("cached read=%d body=%s", status, body)
		}
	}
}

func TestDeletedPostLateCacheFillCannotLeak(t *testing.T) {
	cache, _, cached := visibilityRefreshFixture(t)
	loaded, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	loadPostDetailCache = func(ctx context.Context, key string, _ func() (postResponse, error)) (postResponse, error) {
		return loadJSONCacheWithStoreContext(ctx, key, postCacheTTL, cache.get, cache.set,
			func() (postResponse, error) { close(loaded); <-release; return cached, nil })
	}
	go func() {
		_, err := loadPostDetail(context.Background(), "42")
		done <- err
	}()
	<-loaded
	invalidatePostDeleteDetailCache = func(uint) error { cache.clear(); return nil }
	invalidatePostDetailCacheKey = func(string) error { return errors.New("DEL unavailable after late fill") }
	owner := uint(7)
	ctx, response := newPostDeleteContext("42", &owner)
	DeletePost(ctx)
	close(release)
	if err := <-done; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("in-flight detail returned stale body: %v", err)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d", response.Code)
	}
	if value, _ := cache.get(""); !strings.Contains(value, cached.Content) {
		t.Fatal("late fill was not exercised")
	}
	status, body := visibilityRefreshRead(t)
	if status != http.StatusNotFound || strings.Contains(body, cached.Content) {
		t.Fatalf("late cached read=%d body=%s", status, body)
	}
}

func TestPostDetailVisibilityQueryFailureDoesNotReturnCachedBody(t *testing.T) {
	cache, _, cached := visibilityRefreshFixture(t)
	payload, _ := json.Marshal(cached)
	_ = cache.set("", payload, postCacheTTL)
	global.APIDb = visibilityRefreshDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return nil, errors.New("database unavailable")
	})
	status, body := visibilityRefreshRead(t)
	if status != http.StatusInternalServerError || strings.Contains(body, cached.Content) {
		t.Fatalf("read=%d body=%s", status, body)
	}
}

func TestRefreshProfileErrorsKeepTransientFailureDistinctFromInvalidSession(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"temporary", errors.New("temporary database failure"), 500, "AUTH_INTERNAL"},
		{"deadline", context.DeadlineExceeded, 504, "request timed out"},
		{"missing user", nil, 401, "AUTH_REFRESH_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := visibilityRefreshDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				if test.err != nil {
					return nil, test.err
				}
				return &visibilityRefreshRows{columns: []string{"id", "username", "display_name", "avatar_url"}}, nil
			})
			tokens := &authTokenServiceSpy{}
			controller, err := NewAuthController(db, tokens, allowAllAttemptLimiter{})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"old-token","request_id":"550e8400-e29b-41d4-a716-446655440000"}`))
			ctx.Request.Header.Set("Content-Type", "application/json")
			controller.Refresh(ctx)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) || tokens.rotateCalls != 1 {
				t.Fatalf("status=%d body=%s rotations=%d", response.Code, response.Body.String(), tokens.rotateCalls)
			}
			if tokens.refreshRequestIDs[0] != "550e8400-e29b-41d4-a716-446655440000" {
				t.Fatal("request identity was not passed to token rotation")
			}
		})
	}
}

func TestPostDetailCachedPublicEligibilityIntegration(t *testing.T) {
	for _, state := range []string{"post deleted", "post private", "author deleted"} {
		t.Run(state, func(t *testing.T) {
			db := openReplyIntegrationDatabase(t)
			fixture := newReplyIntegrationFixture(t, db)
			originalCache, originalInvalidator := loadPostDetailCache, invalidatePostDetailCacheKey
			t.Cleanup(func() { loadPostDetailCache, invalidatePostDetailCacheKey = originalCache, originalInvalidator })
			loadPostDetailCache = func(_ context.Context, _ string, loader func() (postResponse, error)) (postResponse, error) {
				return loader()
			}
			id := strconvUint(fixture.Article.ID)
			cached, err := loadPostDetail(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			loadPostDetailCache = func(context.Context, string, func() (postResponse, error)) (postResponse, error) { return cached, nil }
			invalidatePostDetailCacheKey = func(string) error { return errors.New("DEL unavailable") }
			switch state {
			case "post deleted":
				err = db.Model(&models.Post{}).Where("id = ?", fixture.Article.ID).Update("deleted_at", time.Now().UTC()).Error
			case "post private":
				err = db.Model(&models.Post{}).Where("id = ?", fixture.Article.ID).Update("visibility", "private").Error
			case "author deleted":
				err = db.Model(&models.User{}).Where("id = ?", fixture.Author.ID).Update("deleted_at", time.Now().UTC()).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			response, err := loadPostDetail(context.Background(), id)
			if !errors.Is(err, gorm.ErrRecordNotFound) || response.Content != "" {
				t.Fatalf("stale response=%+v error=%v", response, err)
			}
		})
	}
}

func TestRefreshRecoversCommittedRotationAfterProfileFailureIntegration(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if address == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	database := 0
	if raw := os.Getenv("REDIS_TEST_DB"); raw != "" {
		var err error
		database, err = strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: database, Password: os.Getenv("REDIS_TEST_PASSWORD")})
	t.Cleanup(func() { _ = client.Close() })
	store, err := auth.NewRedisRefreshStore(client)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(auth.Config{
		ActiveKID: "recovery-test", PrivateKey: privateKey,
		VerifyKeys: map[string]ed25519.PublicKey{"recovery-test": publicKey},
		Issuer:     "recovery-test", Audience: "recovery-test", AccessTTL: time.Minute,
		RefreshIdleTTL: time.Hour, RefreshAbsoluteTTL: 24 * time.Hour,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"temporary query", "query timeout", "lost response", "user unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			original, err := manager.IssuePair(context.Background(), 42)
			if err != nil {
				t.Fatal(err)
			}
			sessionID := strings.Split(original.RefreshToken, ".")[1]
			t.Cleanup(func() {
				keys, err := client.Keys("auth:refresh:*{" + sessionID + "}*").Result()
				if err != nil {
					t.Error(err)
					return
				}
				if len(keys) > 0 {
					if err := client.Del(keys...).Err(); err != nil {
						t.Error(err)
					}
				}
			})
			var queryError error
			switch scenario {
			case "temporary query":
				queryError = errors.New("database temporarily unavailable")
			case "query timeout":
				queryError = context.DeadlineExceeded
			}
			userAvailable := true
			db := visibilityRefreshDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				if queryError != nil {
					return nil, queryError
				}
				rows := &visibilityRefreshRows{columns: []string{"id", "username", "display_name", "avatar_url"}}
				if userAvailable {
					rows.values = [][]driver.Value{{int64(42), "alice", "Alice", "/avatar.webp"}}
				}
				return rows, nil
			})
			controller, err := NewAuthController(db, manager, allowAllAttemptLimiter{})
			if err != nil {
				t.Fatal(err)
			}
			requestID := uuid.NewString()
			refresh := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(response)
				body, _ := json.Marshal(map[string]string{"refresh_token": original.RefreshToken, "request_id": requestID})
				ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(string(body)))
				ctx.Request.Header.Set("Content-Type", "application/json")
				controller.Refresh(ctx)
				return response
			}
			first := refresh()
			want := http.StatusOK
			if scenario == "temporary query" {
				want = 500
			}
			if scenario == "query timeout" {
				want = 504
			}
			if first.Code != want {
				t.Fatalf("first status=%d want=%d", first.Code, want)
			}
			queryError = nil
			if scenario == "user unavailable" {
				userAvailable = false
			}
			second := refresh()
			if !userAvailable {
				if second.Code != 401 || !strings.Contains(second.Body.String(), "AUTH_REFRESH_INVALID") {
					t.Fatalf("inactive user retry status=%d", second.Code)
				}
				return
			}
			if second.Code != 200 {
				t.Fatalf("recovery status=%d", second.Code)
			}
			var result authResponse
			if err := json.Unmarshal(second.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.RefreshToken == original.RefreshToken || result.User.ID != 42 {
				t.Fatal("recovery did not return the committed user and successor")
			}
			if scenario == "lost response" {
				var previous authResponse
				_ = json.Unmarshal(first.Body.Bytes(), &previous)
				if result.RefreshToken != previous.RefreshToken {
					t.Fatal("retry did not recover the lost successor")
				}
			}
			if _, err := manager.RotateRefresh(context.Background(), result.RefreshToken, uuid.NewString()); err != nil {
				t.Fatalf("recovered successor unusable: %v", err)
			}
		})
	}
}
