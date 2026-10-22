package controllers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/auth"
	"Go.exchange/internal/testdb"
	"Go.exchange/middlewares"
	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type passwordIntegration struct {
	db         *gorm.DB
	redis      *redis.Client
	tokens     *auth.Manager
	controller *AuthController
}

func openPasswordIntegration(t *testing.T) passwordIntegration {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if dsn == "" || address == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN and REDIS_TEST_ADDR are required for password security integration tests")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(); err != nil {
		t.Fatalf("PostgreSQL ping: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	database := 0
	if raw := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); raw != "" {
		database, err = strconv.Atoi(raw)
		if err != nil || database < 0 {
			t.Fatalf("invalid REDIS_TEST_DB: %q", raw)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("REDIS_TEST_PASSWORD"), DB: database})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatalf("Redis ping: %v", err)
	}
	refreshStore, err := auth.NewRedisRefreshStore(client)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewManager(auth.Config{
		ActiveKID: "password-test-v1", PrivateKey: privateKey,
		VerifyKeys: map[string]ed25519.PublicKey{"password-test-v1": publicKey},
		Issuer:     "go.exchange.password.test", Audience: "go.exchange.password.test.api",
		AccessTTL: 15 * time.Minute, RefreshIdleTTL: time.Hour, RefreshAbsoluteTTL: 24 * time.Hour,
	}, refreshStore)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := auth.NewRedisAttemptLimiter(client)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewAuthController(db, tokens, limiter)
	if err != nil {
		t.Fatal(err)
	}
	return passwordIntegration{db: db, redis: client, tokens: tokens, controller: controller}
}

func (integration passwordIntegration) routers(t *testing.T) (*gin.Engine, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	versionReader, err := auth.NewGormAuthVersionReader(integration.db)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVersionedAccessTokenVerifier(integration.tokens, versionReader)
	if err != nil {
		t.Fatal(err)
	}
	protected := gin.New()
	protected.Use(middlewares.AuthMiddleware(verifier))
	protected.PUT("/api/me/password", integration.controller.ChangePassword)
	protected.GET("/api/protected", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	public := gin.New()
	public.POST("/api/auth/login", integration.controller.Login)
	public.POST("/api/auth/refresh", integration.controller.Refresh)
	return protected, public
}

func passwordIntegrationRequest(engine *gin.Engine, method, path, body, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func loginPasswordIntegration(t *testing.T, engine *gin.Engine, username, password string) (*httptest.ResponseRecorder, authResponse) {
	t.Helper()
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	response := passwordIntegrationRequest(engine, http.MethodPost, "/api/auth/login", string(body), "")
	if response.Code != http.StatusOK {
		return response, authResponse{}
	}
	var decoded authResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return response, decoded
}

func TestChangePasswordInvalidatesEveryDeviceAndChangesLoginPasswordIntegration(t *testing.T) {
	integration := openPasswordIntegration(t)
	protected, public := integration.routers(t)
	oldPassword := "old-password-123"
	hash, err := utils.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "password-user-" + uuid.NewString(), Password: hash, AuthVersion: 1}
	if err := integration.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integration.db.Unscoped().Delete(&user) })

	loginAResponse, deviceA := loginPasswordIntegration(t, public, user.Username, oldPassword)
	loginBResponse, deviceB := loginPasswordIntegration(t, public, user.Username, oldPassword)
	if loginAResponse.Code != http.StatusOK || loginBResponse.Code != http.StatusOK {
		t.Fatalf("device login status A=%d B=%d", loginAResponse.Code, loginBResponse.Code)
	}
	for _, token := range []string{deviceA.AccessToken, deviceB.AccessToken} {
		if response := passwordIntegrationRequest(protected, http.MethodGet, "/api/protected", "", "Bearer "+token); response.Code != http.StatusNoContent {
			t.Fatalf("pre-change access token status=%d body=%s", response.Code, response.Body.String())
		}
	}
	assertCredentialsUnchanged := func(label string) {
		t.Helper()
		var unchanged models.User
		if err := integration.db.Select("id", "password", "auth_version").First(&unchanged, user.ID).Error; err != nil {
			t.Fatal(err)
		}
		if unchanged.AuthVersion != 1 || unchanged.Password != hash {
			t.Fatalf("%s changed persisted credentials: auth_version=%d password_unchanged=%t", label, unchanged.AuthVersion, unchanged.Password == hash)
		}
	}

	wrongCurrent := passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", `{"current_password":"wrong-current","new_password":"candidate-password"}`, "Bearer "+deviceA.AccessToken)
	if wrongCurrent.Code != http.StatusBadRequest || !strings.Contains(wrongCurrent.Body.String(), `"code":"AUTH_CURRENT_PASSWORD_INVALID"`) {
		t.Fatalf("wrong current password status=%d body=%s", wrongCurrent.Code, wrongCurrent.Body.String())
	}
	assertCredentialsUnchanged("wrong current password")

	unchangedPassword := passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", `{"current_password":"old-password-123","new_password":"old-password-123"}`, "Bearer "+deviceA.AccessToken)
	if unchangedPassword.Code != http.StatusBadRequest || !strings.Contains(unchangedPassword.Body.String(), `"code":"AUTH_PASSWORD_UNCHANGED"`) {
		t.Fatalf("same password status=%d body=%s", unchangedPassword.Code, unchangedPassword.Body.String())
	}
	assertCredentialsUnchanged("same password")

	originalHasher := integration.controller.hashPassword
	integration.controller.hashPassword = func(string) (string, error) { return "", errors.New("injected bcrypt failure") }
	hashFailure := passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", `{"current_password":"old-password-123","new_password":"candidate-password"}`, "Bearer "+deviceA.AccessToken)
	integration.controller.hashPassword = originalHasher
	if hashFailure.Code != http.StatusInternalServerError || !strings.Contains(hashFailure.Body.String(), `"code":"AUTH_INTERNAL"`) {
		t.Fatalf("bcrypt failure status=%d body=%s", hashFailure.Code, hashFailure.Body.String())
	}
	assertCredentialsUnchanged("bcrypt failure")

	change := `{"current_password":"old-password-123","new_password":"new-password-456"}`
	response := passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", change, "Bearer "+deviceA.AccessToken)
	if response.Code != http.StatusNoContent || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("change status=%d cache-control=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var saved models.User
	if err := integration.db.Select("id", "password", "auth_version").First(&saved, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 2 || !utils.CheckPassword("new-password-456", saved.Password) || utils.CheckPassword(oldPassword, saved.Password) {
		t.Fatalf("password transaction result auth_version=%d password_hash_valid_new=%t", saved.AuthVersion, utils.CheckPassword("new-password-456", saved.Password))
	}
	for label, token := range map[string]string{"device A": deviceA.AccessToken, "device B": deviceB.AccessToken} {
		response := passwordIntegrationRequest(protected, http.MethodGet, "/api/protected", "", "Bearer "+token)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"AUTH_TOKEN_INVALID"`) {
			t.Fatalf("%s stale access status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	for label, token := range map[string]string{"device A": deviceA.RefreshToken, "device B": deviceB.RefreshToken} {
		payload, _ := json.Marshal(refreshRequest{RefreshToken: token})
		response := passwordIntegrationRequest(public, http.MethodPost, "/api/auth/refresh", string(payload), "")
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"AUTH_REFRESH_INVALID"`) {
			t.Fatalf("%s stale refresh status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	oldLogin, _ := loginPasswordIntegration(t, public, user.Username, oldPassword)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status=%d body=%s", oldLogin.Code, oldLogin.Body.String())
	}
	newLogin, signedIn := loginPasswordIntegration(t, public, user.Username, "new-password-456")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login status=%d body=%s", newLogin.Code, newLogin.Body.String())
	}
	claims, err := integration.tokens.VerifyAccess(signedIn.AccessToken)
	if err != nil || claims.AuthVersion != 2 {
		t.Fatalf("new login auth version claims=%+v error=%v", claims, err)
	}
}

func TestConcurrentPasswordChangesRevalidateLockedCurrentPasswordIntegration(t *testing.T) {
	integration := openPasswordIntegration(t)
	protected, public := integration.routers(t)
	oldPassword := "old-password-concurrent"
	hash, err := utils.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "password-race-" + uuid.NewString(), Password: hash, AuthVersion: 1}
	if err := integration.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integration.db.Unscoped().Delete(&user) })
	loginResponse, session := loginPasswordIntegration(t, public, user.Username, oldPassword)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for _, next := range []string{"new-password-a", "new-password-b"} {
		wait.Add(1)
		go func(next string) {
			defer wait.Done()
			<-start
			payload := `{"current_password":"` + oldPassword + `","new_password":"` + next + `"}`
			responses <- passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", payload, "Bearer "+session.AccessToken)
		}(next)
	}
	close(start)
	wait.Wait()
	close(responses)
	statuses := map[int]int{}
	for response := range responses {
		statuses[response.Code]++
	}
	if statuses[http.StatusNoContent] != 1 || statuses[http.StatusBadRequest]+statuses[http.StatusUnauthorized] != 1 {
		t.Fatalf("concurrent password change statuses=%v, want one 204 and one rejected request", statuses)
	}
	var saved models.User
	if err := integration.db.Select("id", "password", "auth_version").First(&saved, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 2 || !(utils.CheckPassword("new-password-a", saved.Password) || utils.CheckPassword("new-password-b", saved.Password)) {
		t.Fatalf("concurrent update lost auth version or password state: auth_version=%d", saved.AuthVersion)
	}
}

func TestPasswordChangeRefreshRaceDoesNotResurrectOldVersionIntegration(t *testing.T) {
	integration := openPasswordIntegration(t)
	protected, public := integration.routers(t)
	oldPassword := "refresh-race-current"
	hash, err := utils.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "password-refresh-race-" + uuid.NewString(), Password: hash, AuthVersion: 1}
	if err := integration.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integration.db.Unscoped().Delete(&user) })

	deviceAResponse, deviceA := loginPasswordIntegration(t, public, user.Username, oldPassword)
	deviceBResponse, deviceB := loginPasswordIntegration(t, public, user.Username, oldPassword)
	deviceCResponse, deviceC := loginPasswordIntegration(t, public, user.Username, oldPassword)
	if deviceAResponse.Code != http.StatusOK || deviceBResponse.Code != http.StatusOK || deviceCResponse.Code != http.StatusOK {
		t.Fatalf("device login statuses A=%d B=%d C=%d", deviceAResponse.Code, deviceBResponse.Code, deviceCResponse.Code)
	}

	beforeResponse, beforePair := refreshPasswordIntegration(t, public, deviceA.RefreshToken)
	if beforeResponse.Code != http.StatusOK {
		t.Fatalf("pre-change refresh status=%d body=%s", beforeResponse.Code, beforeResponse.Body.String())
	}
	beforeClaims, err := integration.tokens.VerifyAccess(beforePair.AccessToken)
	if err != nil || beforeClaims.AuthVersion != 1 {
		t.Fatalf("pre-change refresh auth version claims=%+v error=%v", beforeClaims, err)
	}

	originalHasher := integration.controller.hashPassword
	hashStarted := make(chan struct{})
	releaseHash := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHash) }) }
	t.Cleanup(release)
	var enteredOnce sync.Once
	integration.controller.hashPassword = func(password string) (string, error) {
		enteredOnce.Do(func() { close(hashStarted) })
		<-releaseHash
		return originalHasher(password)
	}
	changeResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := `{"current_password":"refresh-race-current","new_password":"refresh-race-next"}`
		changeResult <- passwordIntegrationRequest(protected, http.MethodPut, "/api/me/password", body, "Bearer "+beforePair.AccessToken)
	}()
	select {
	case <-hashStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("password change did not reach bcrypt hashing")
	}

	duringResponse, duringPair := refreshPasswordIntegration(t, public, deviceB.RefreshToken)
	if duringResponse.Code != http.StatusOK {
		t.Fatalf("in-flight refresh status=%d body=%s", duringResponse.Code, duringResponse.Body.String())
	}
	duringClaims, err := integration.tokens.VerifyAccess(duringPair.AccessToken)
	if err != nil || duringClaims.AuthVersion != 1 {
		t.Fatalf("in-flight refresh upgraded auth version claims=%+v error=%v", duringClaims, err)
	}
	release()
	changeResponse := <-changeResult
	integration.controller.hashPassword = originalHasher
	if changeResponse.Code != http.StatusNoContent {
		t.Fatalf("password change status=%d body=%s", changeResponse.Code, changeResponse.Body.String())
	}

	var saved models.User
	if err := integration.db.Select("id", "auth_version").First(&saved, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 2 {
		t.Fatalf("auth_version=%d, want 2", saved.AuthVersion)
	}
	for label, token := range map[string]string{
		"pre-change access":   beforePair.AccessToken,
		"in-flight access":    duringPair.AccessToken,
		"other device access": deviceC.AccessToken,
	} {
		response := passwordIntegrationRequest(protected, http.MethodGet, "/api/protected", "", "Bearer "+token)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	for label, token := range map[string]string{
		"pre-change refresh":  beforePair.RefreshToken,
		"in-flight refresh":   duringPair.RefreshToken,
		"post-change refresh": deviceC.RefreshToken,
	} {
		response, _ := refreshPasswordIntegration(t, public, token)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"AUTH_REFRESH_INVALID"`) {
			t.Fatalf("%s status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
}

func refreshPasswordIntegration(t *testing.T, engine *gin.Engine, token string) (*httptest.ResponseRecorder, authResponse) {
	t.Helper()
	payload, err := json.Marshal(refreshRequest{RefreshToken: token})
	if err != nil {
		t.Fatal(err)
	}
	response := passwordIntegrationRequest(engine, http.MethodPost, "/api/auth/refresh", string(payload), "")
	if response.Code != http.StatusOK {
		return response, authResponse{}
	}
	var decoded authResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return response, decoded
}

func TestAuthenticationVersionReaderRejectsSoftDeletedUserIntegration(t *testing.T) {
	integration := openPasswordIntegration(t)
	protected, _ := integration.routers(t)
	user := models.User{Username: "password-deleted-user-" + uuid.NewString(), Password: "unused", AuthVersion: 1}
	if err := integration.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integration.db.Unscoped().Delete(&user) })
	pair, err := integration.tokens.IssuePair(context.Background(), user.ID, user.AuthVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := integration.db.Delete(&user).Error; err != nil {
		t.Fatal(err)
	}
	response := passwordIntegrationRequest(protected, http.MethodGet, "/api/protected", "", "Bearer "+pair.AccessToken)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"AUTH_TOKEN_INVALID"`) {
		t.Fatalf("soft-deleted user access status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPasswordTransactionFailureRollsBackPasswordAndAuthVersionIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN is required for transaction rollback integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	schema := "password_rollback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := tx.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`SET LOCAL search_path TO "` + schema + `", public`).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`
CREATE OR REPLACE FUNCTION reject_password_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.password <> OLD.password THEN RAISE EXCEPTION 'password update rejected for test'; END IF;
  RETURN NEW;
END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`CREATE TRIGGER reject_password_update BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_password_change()`).Error; err != nil {
		t.Fatal(err)
	}
	oldHash, err := utils.HashPassword("rollback-current")
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "password-rollback", Password: oldHash, AuthVersion: 1}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	controller, err := NewAuthController(tx, stubTokenService{}, allowAllAttemptLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := callPasswordHandler(controller, `{"current_password":"rollback-current","new_password":"rollback-new"}`, user.ID)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("rollback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved models.User
	if err := tx.Select("id", "password", "auth_version").First(&saved, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 1 || saved.Password != oldHash {
		t.Fatalf("failed transaction changed password/version: auth_version=%d password_same=%t", saved.AuthVersion, saved.Password == oldHash)
	}
}

func TestAuthenticationVersionDatabaseFailureReturns503Integration(t *testing.T) {
	integration := openPasswordIntegration(t)
	hash, err := utils.HashPassword("database-failure-current")
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "password-db-failure-" + uuid.NewString(), Password: hash, AuthVersion: 1}
	if err := integration.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { integration.db.Unscoped().Delete(&user) })
	pair, err := integration.tokens.IssuePair(context.Background(), user.ID, user.AuthVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = integration.tokens.RevokeRefresh(context.Background(), pair.RefreshToken) })
	reader, err := auth.NewGormAuthVersionReader(integration.db)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVersionedAccessTokenVerifier(integration.tokens, reader)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := integration.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := integration.db.Unscoped().Delete(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewares.AuthMiddleware(verifier))
	engine.GET("/api/protected", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	response := passwordIntegrationRequest(engine, http.MethodGet, "/api/protected", "", "Bearer "+pair.AccessToken)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"AUTH_VERSION_UNAVAILABLE"`) {
		t.Fatalf("database failure status=%d body=%s", response.Code, response.Body.String())
	}
}
