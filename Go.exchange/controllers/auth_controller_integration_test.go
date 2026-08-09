package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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

	originalDB := global.Db
	originalSaveRefreshToken := saveRefreshToken
	global.Db = db
	saveRefreshToken = func(uint, string) error { return nil }
	t.Cleanup(func() {
		global.Db = originalDB
		saveRefreshToken = originalSaveRefreshToken
	})

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
	Register(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var user models.User
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&user)
	})

	if user.Username != username {
		t.Fatalf("username=%q, want %q", user.Username, username)
	}
	if user.Password == "" {
		t.Fatal("stored password is empty")
	}
	if !utils.CheckPassword(password, user.Password) {
		t.Fatal("stored password hash does not match submitted password")
	}
}

func TestRegisterRequestBindsCredentials(t *testing.T) {
	payload := []byte(`{"username":"alice","password":"secret123"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	var req registerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		t.Fatalf("bind registration request: %v", err)
	}
	if req.Username != "alice" || req.Password != "secret123" {
		t.Fatalf("bound request=%+v", req)
	}
}
