package controllers

import (
	"Go.exchange/auth"
	"Go.exchange/models"
	"Go.exchange/utils"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type stubTokenService struct{}

type allowAllAttemptLimiter struct{}

func (allowAllAttemptLimiter) Allow(context.Context, auth.AttemptInput) (auth.AttemptDecision, error) {
	return auth.AttemptDecision{Allowed: true}, nil
}

func (stubTokenService) IssuePair(_ context.Context, userID uint) (auth.TokenPair, error) {
	return auth.TokenPair{
		UserID:           userID,
		AccessToken:      "raw-access-token",
		RefreshToken:     "rt1.00000000-0000-4000-8000-000000000000.test-secret",
		TokenType:        "Bearer",
		AccessExpiresIn:  15 * time.Minute,
		RefreshExpiresIn: 7 * 24 * time.Hour,
	}, nil
}

func (stubTokenService) RotateRefresh(_ context.Context, _ string) (auth.TokenPair, error) {
	return auth.TokenPair{}, auth.ErrRefreshInvalid
}

func (stubTokenService) VerifyAccess(_ string) (*auth.AccessClaims, error) {
	return nil, auth.ErrAccessTokenInvalid
}

func TestRegisterStoresSubmittedPasswordIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	controller, err := NewAuthController(db, stubTokenService{}, allowAllAttemptLimiter{})
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
