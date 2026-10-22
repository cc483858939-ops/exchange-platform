package controllers

import (
	"Go.exchange/auth"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"
	"Go.exchange/utils"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type stubTokenService struct{}

type refreshTokenService struct {
	userID      uint
	authVersion int64
}

type allowAllAttemptLimiter struct{}

type userLikeInitializerSpy struct {
	userIDs []uint
	err     error
}

func (spy *userLikeInitializerSpy) InitializeUserEmpty(_ context.Context, userID uint) error {
	spy.userIDs = append(spy.userIDs, userID)
	return spy.err
}

func (allowAllAttemptLimiter) Allow(context.Context, auth.AttemptInput) (auth.AttemptDecision, error) {
	return auth.AttemptDecision{Allowed: true}, nil
}

func (stubTokenService) IssuePair(_ context.Context, userID uint, authVersion int64) (auth.TokenPair, error) {
	return auth.TokenPair{
		UserID:           userID,
		AuthVersion:      authVersion,
		AccessToken:      "raw-access-token",
		RefreshToken:     "rt1.00000000-0000-4000-8000-000000000000.test-secret",
		TokenType:        "Bearer",
		AccessExpiresIn:  15 * time.Minute,
		RefreshExpiresIn: 7 * 24 * time.Hour,
	}, nil
}

func (stubTokenService) RotateRefresh(_ context.Context, _, _ string) (auth.TokenPair, error) {
	return auth.TokenPair{}, auth.ErrRefreshInvalid
}

func (stubTokenService) VerifyAccess(_ string) (*auth.AccessClaims, error) {
	return nil, auth.ErrAccessTokenInvalid
}

func (service refreshTokenService) IssuePair(_ context.Context, userID uint, authVersion int64) (auth.TokenPair, error) {
	return stubTokenService{}.IssuePair(context.Background(), userID, authVersion)
}

func (service refreshTokenService) RotateRefresh(_ context.Context, _, _ string) (auth.TokenPair, error) {
	return auth.TokenPair{
		UserID:           service.userID,
		AuthVersion:      service.authVersion,
		AccessToken:      "rotated-access-token",
		RefreshToken:     "rt1.00000000-0000-4000-8000-000000000001.rotated-secret",
		TokenType:        "Bearer",
		AccessExpiresIn:  15 * time.Minute,
		RefreshExpiresIn: 7 * 24 * time.Hour,
	}, nil
}

func (refreshTokenService) VerifyAccess(_ string) (*auth.AccessClaims, error) {
	return nil, auth.ErrAccessTokenInvalid
}

func TestRegisterStoresSubmittedPasswordIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserOnboarding{}, &models.UserOnboardingInterest{}); err != nil {
		t.Fatal(err)
	}
	userLikeInit := &userLikeInitializerSpy{}
	controller, err := NewAuthController(db, stubTokenService{}, allowAllAttemptLimiter{}, userLikeInit)
	if err != nil {
		t.Fatal(err)
	}

	username := "register-" + uuid.NewString()
	password := "secret123"
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.Register(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response authResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != "raw-access-token" || response.TokenType != "Bearer" {
		t.Fatalf("unexpected auth response: %+v", response)
	}
	if response.User.Username != username || response.User.ID == 0 {
		t.Fatalf("unexpected response user: %+v", response.User)
	}

	var user models.User
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&user)
	})
	if user.Password == "" || !utils.CheckPassword(password, user.Password) {
		t.Fatal("stored password hash does not match submitted password")
	}
	if len(userLikeInit.userIDs) != 1 || userLikeInit.userIDs[0] != user.ID {
		t.Fatalf("User Like initializer IDs=%v want [%d]", userLikeInit.userIDs, user.ID)
	}
	var onboarding models.UserOnboarding
	if err := db.Where("user_id = ?", user.ID).Take(&onboarding).Error; err != nil {
		t.Fatalf("load registered user's onboarding state: %v", err)
	}
	if onboarding.CurrentStep != models.UserOnboardingStepAvatar || onboarding.CompletedAt != nil {
		t.Fatalf("unexpected initial onboarding state: %+v", onboarding)
	}
	var interestCount int64
	if err := db.Model(&models.UserOnboardingInterest{}).Where("user_id = ?", user.ID).Count(&interestCount).Error; err != nil || interestCount != 0 {
		t.Fatalf("initial onboarding interests=%d err=%v", interestCount, err)
	}
	duplicateRecorder := httptest.NewRecorder()
	duplicateCtx, _ := gin.CreateTestContext(duplicateRecorder)
	duplicateCtx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	duplicateCtx.Request.Header.Set("Content-Type", "application/json")
	controller.Register(duplicateCtx)
	if duplicateRecorder.Code != http.StatusConflict {
		t.Fatalf("duplicate username status=%d body=%s", duplicateRecorder.Code, duplicateRecorder.Body.String())
	}
	assertAuthError(t, duplicateRecorder, "AUTH_USERNAME_UNAVAILABLE")
}

func TestRegisterOverBcryptPasswordLimitHasNoDatabaseSideEffectsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })

	schema := "register_over_limit_" + uuid.NewString()
	if err := tx.Exec("CREATE SCHEMA \"" + schema + "\"").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO \"" + schema + "\", public").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}, &models.UserOnboarding{}, &models.UserOnboardingInterest{}); err != nil {
		t.Fatal(err)
	}

	var onboardingBefore, interestsBefore int64
	if err := tx.Model(&models.UserOnboarding{}).Count(&onboardingBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Model(&models.UserOnboardingInterest{}).Count(&interestsBefore).Error; err != nil {
		t.Fatal(err)
	}

	limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
	tokens := &authTokenServiceSpy{}
	userLikeInit := &userLikeInitializerSpy{}
	controller, err := NewAuthController(tx, tokens, limiter, userLikeInit)
	if err != nil {
		t.Fatal(err)
	}
	username := "register-over-limit-" + uuid.NewString()
	password := strings.Repeat("a", 73)
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.Register(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAuthError(t, recorder, "AUTH_PASSWORD_TOO_LONG")
	var response struct {
		Code      string `json:"code"`
		Error     string `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode registration error response: %v; body=%s", err, recorder.Body.String())
	}
	if response.Code == "AUTH_INTERNAL" || response.Error != "Password must be 72 bytes or fewer." || response.RequestID == "" {
		t.Fatalf("unexpected registration error response: %+v", response)
	}

	var userCount int64
	if err := tx.Model(&models.User{}).Where("username = ?", username).Count(&userCount).Error; err != nil || userCount != 0 {
		t.Fatalf("over-limit registration left user count=%d err=%v", userCount, err)
	}
	var onboardingAfter, interestsAfter int64
	if err := tx.Model(&models.UserOnboarding{}).Count(&onboardingAfter).Error; err != nil || onboardingAfter != onboardingBefore {
		t.Fatalf("over-limit registration changed onboarding count from %d to %d: %v", onboardingBefore, onboardingAfter, err)
	}
	if err := tx.Model(&models.UserOnboardingInterest{}).Count(&interestsAfter).Error; err != nil || interestsAfter != interestsBefore {
		t.Fatalf("over-limit registration changed onboarding interest count from %d to %d: %v", interestsBefore, interestsAfter, err)
	}
	if len(userLikeInit.userIDs) != 0 {
		t.Fatalf("over-limit registration initialized user likes: %v", userLikeInit.userIDs)
	}
	if tokens.issueCalls != 0 || tokens.rotateCalls != 0 {
		t.Fatalf("over-limit registration issued or rotated tokens: issue=%d rotate=%d", tokens.issueCalls, tokens.rotateCalls)
	}
	if len(limiter.inputs) != 0 {
		t.Fatalf("over-limit registration reached limiter: %+v", limiter.inputs)
	}
}

func TestRegisterOnboardingInitializationFailureRollsBackUserIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := "register_onboarding_failure_" + uuid.NewString()
	if err := tx.Exec("CREATE SCHEMA \"" + schema + "\"").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO \"" + schema + "\", public").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	initializer := &userLikeInitializerSpy{}
	tokens := &authTokenServiceSpy{}
	controller, err := NewAuthController(tx, tokens, allowAllAttemptLimiter{}, initializer)
	if err != nil {
		t.Fatal(err)
	}
	username := "onboarding-init-fail-" + uuid.NewString()
	payload, err := json.Marshal(map[string]string{"username": username, "password": "secret123"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.Register(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAuthError(t, recorder, "AUTH_ONBOARDING_INIT_FAILED")
	if len(initializer.userIDs) != 0 || tokens.issueCalls != 0 {
		t.Fatalf("continued registration after onboarding initialization failure: like=%v tokenCalls=%d", initializer.userIDs, tokens.issueCalls)
	}
	var userCount int64
	if err := tx.Model(&models.User{}).Where("username = ?", username).Count(&userCount).Error; err != nil || userCount != 0 {
		t.Fatalf("failed registration left user count=%d err=%v", userCount, err)
	}
}

func TestRegisterRedisInitializationFailureRollsBackUserIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserOnboarding{}, &models.UserOnboardingInterest{}); err != nil {
		t.Fatal(err)
	}
	username := "register-like-fail-" + uuid.NewString()
	initializer := &userLikeInitializerSpy{err: errors.New("Redis unavailable")}
	tokens := &authTokenServiceSpy{}
	controller, err := NewAuthController(db, tokens, allowAllAttemptLimiter{}, initializer)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"username": username, "password": "secret123"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.Register(ctx)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if tokens.issueCalls != 0 {
		t.Fatalf("issued %d token pairs after Redis initialization failure", tokens.issueCalls)
	}
	var user models.User
	if err := db.Unscoped().Where("username = ?", username).First(&user).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed registration left User=%#v err=%v", user, err)
	}
	if len(initializer.userIDs) != 1 {
		t.Fatalf("User Like initializer IDs=%v want one attempted user", initializer.userIDs)
	}
	var onboarding models.UserOnboarding
	if err := db.Where("user_id = ?", initializer.userIDs[0]).Take(&onboarding).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed registration left onboarding state=%+v err=%v", onboarding, err)
	}
}

func TestRefreshPreservesProfileIdentityIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:    "refresh-user-" + uuid.NewString(),
		Password:    "unused",
		AuthVersion: 1,
		DisplayName: "Refresh User",
		AvatarURL:   "/api/files/profile-avatars/refresh/test.webp",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&user)
	})
	controller, err := NewAuthController(db, refreshTokenService{userID: user.ID, authVersion: user.AuthVersion}, allowAllAttemptLimiter{})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewBufferString(`{"refresh_token":"rt1.original"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.Refresh(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response authResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.User.ID != user.ID || response.User.Username != user.Username {
		t.Fatalf("unexpected response user identity: %+v", response.User)
	}
	if response.User.DisplayName != user.DisplayName || response.User.AvatarURL != user.AvatarURL {
		t.Fatalf("refresh lost profile identity: %+v", response.User)
	}
}

func TestRegisterRequestBindsCredentials(t *testing.T) {
	payload := []byte(`{"username":"alice","password":"secret123"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	var request registerRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		t.Fatalf("bind registration request: %v", err)
	}
	if request.Username != "alice" || request.Password != "secret123" {
		t.Fatalf("bound request=%+v", request)
	}
}
