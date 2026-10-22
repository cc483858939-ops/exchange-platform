package controllers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/auth"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestValidateNewPasswordLengthUsesUTF8Bytes(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "ASCII 72", password: strings.Repeat("a", 72)},
		{name: "ASCII 73", password: strings.Repeat("a", 73), wantErr: true},
		{name: "CJK 24", password: strings.Repeat("汉", 24)},
		{name: "CJK 25", password: strings.Repeat("汉", 25), wantErr: true},
		{name: "emoji 18", password: strings.Repeat("😀", 18)},
		{name: "emoji 19", password: strings.Repeat("😀", 19), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateNewPasswordLength(test.password)
			if errors.Is(err, bcrypt.ErrPasswordTooLong) != test.wantErr {
				t.Fatalf("password bytes=%d error=%v wantErr=%t", len([]byte(test.password)), err, test.wantErr)
			}
		})
	}
}

func TestPasswordVerificationSeparatesWrongInputFromStoredHashFailure(t *testing.T) {
	hash, err := utils.HashPassword("current password")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySubmittedCurrentPassword("wrong password", hash); !errors.Is(err, errCurrentPasswordInvalid) {
		t.Fatalf("wrong current password error=%v", err)
	}
	if err := verifySubmittedCurrentPassword("current password", "malformed-bcrypt-hash"); err == nil || errors.Is(err, errCurrentPasswordInvalid) {
		t.Fatalf("malformed stored hash was misclassified: %v", err)
	}
	if err := ensureNewPasswordDiffers("current password", "current password"); !errors.Is(err, errPasswordUnchanged) {
		t.Fatalf("same password error=%v", err)
	}
	if err := ensureNewPasswordDiffers("current password", "a different password"); err != nil {
		t.Fatalf("different password error=%v", err)
	}
}

func TestChangePasswordRejectsRequiredAndUnauthenticatedRequestsBeforeLimiter(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		userID  uint
		want    string
	}{
		{name: "missing current", payload: `{"new_password":"new"}`, userID: 42, want: "AUTH_REQUEST_INVALID"},
		{name: "missing new", payload: `{"current_password":"old"}`, userID: 42, want: "AUTH_REQUEST_INVALID"},
		{name: "unauthenticated", payload: `{"current_password":"old","new_password":"new"}`, want: "AUTH_TOKEN_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
			controller := &AuthController{db: &gorm.DB{}, tokens: &authTokenServiceSpy{}, limiter: limiter}
			response := callPasswordHandler(controller, test.payload, test.userID)
			if response.Code != http.StatusBadRequest && test.want != "AUTH_TOKEN_INVALID" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.want == "AUTH_TOKEN_INVALID" && response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAuthError(t, response, test.want)
			if len(limiter.inputs) != 0 {
				t.Fatalf("invalid request reached limiter: %+v", limiter.inputs)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control=%q, want no-store", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestChangePasswordRateLimiterFailsClosedAndUsesUserID(t *testing.T) {
	payload := `{"current_password":"old","new_password":"new"}`
	for _, test := range []struct {
		name     string
		limiter  *authLimiterSpy
		status   int
		wantCode string
	}{
		{name: "limited", limiter: &authLimiterSpy{decision: auth.AttemptDecision{RetryAfter: 1500 * time.Millisecond}}, status: http.StatusTooManyRequests, wantCode: "AUTH_RATE_LIMITED"},
		{name: "limiter unavailable", limiter: &authLimiterSpy{err: errors.New("Redis unavailable")}, status: http.StatusServiceUnavailable, wantCode: "AUTH_RATE_LIMIT_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &AuthController{db: &gorm.DB{}, tokens: &authTokenServiceSpy{}, limiter: test.limiter}
			response := callPasswordHandler(controller, payload, 42)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAuthError(t, response, test.wantCode)
			if len(test.limiter.inputs) != 1 || test.limiter.inputs[0].Action != auth.AttemptChangePassword || test.limiter.inputs[0].Subject != "42" {
				t.Fatalf("limiter input=%+v", test.limiter.inputs)
			}
			if strings.Contains(test.limiter.inputs[0].Subject, "old") || strings.Contains(test.limiter.inputs[0].Subject, "new") {
				t.Fatalf("password leaked into limiter subject: %+v", test.limiter.inputs[0])
			}
		})
	}
}

func callPasswordHandler(controller *AuthController, payload string, userID uint) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/me/password", bytes.NewBufferString(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		ctx.Set("user_id", userID)
	}
	controller.ChangePassword(ctx)
	return response
}
