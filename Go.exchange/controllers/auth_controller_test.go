package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type authLimiterSpy struct {
	decision auth.AttemptDecision
	err      error
	inputs   []auth.AttemptInput
}

func (spy *authLimiterSpy) Allow(_ context.Context, input auth.AttemptInput) (auth.AttemptDecision, error) {
	spy.inputs = append(spy.inputs, input)
	return spy.decision, spy.err
}

type authTokenServiceSpy struct {
	issueCalls  int
	rotateCalls int
}

func (spy *authTokenServiceSpy) IssuePair(_ context.Context, userID uint) (auth.TokenPair, error) {
	spy.issueCalls++
	return auth.TokenPair{
		UserID:           userID,
		AccessToken:      "access-token",
		RefreshToken:     "rt1.test-refresh-token",
		TokenType:        "Bearer",
		AccessExpiresIn:  15 * time.Minute,
		RefreshExpiresIn: 7 * 24 * time.Hour,
	}, nil
}

func (spy *authTokenServiceSpy) RotateRefresh(_ context.Context, _ string) (auth.TokenPair, error) {
	spy.rotateCalls++
	return auth.TokenPair{
		UserID:           42,
		AccessToken:      "access-token",
		RefreshToken:     "rt1.rotated-refresh-token",
		TokenType:        "Bearer",
		AccessExpiresIn:  15 * time.Minute,
		RefreshExpiresIn: 7 * 24 * time.Hour,
	}, nil
}

func (spy *authTokenServiceSpy) VerifyAccess(string) (*auth.AccessClaims, error) {
	return nil, auth.ErrAccessTokenInvalid
}

func TestNewAuthControllerRequiresAllDependencies(t *testing.T) {
	allow := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
	tokens := &authTokenServiceSpy{}
	db := &gorm.DB{}
	for _, test := range []struct {
		name  string
		db    *gorm.DB
		token auth.TokenService
		limit auth.AttemptLimiter
	}{
		{name: "database", db: nil, token: tokens, limit: allow},
		{name: "token service", db: db, token: nil, limit: allow},
		{name: "attempt limiter", db: db, token: tokens, limit: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewAuthController(test.db, test.token, test.limit); err == nil {
				t.Fatal("NewAuthController unexpectedly succeeded")
			}
		})
	}
}

func TestAuthControllerLimiterDenyReturns429BeforeExpensiveOperations(t *testing.T) {
	for _, test := range []struct {
		name    string
		action  auth.AttemptAction
		payload []byte
		handle  func(*AuthController, *gin.Context)
	}{
		{name: "register", action: auth.AttemptRegister, payload: []byte(`{"username":"Alice","password":"secret123"}`), handle: (*AuthController).Register},
		{name: "login", action: auth.AttemptLogin, payload: []byte(`{"username":"Alice","password":"secret123"}`), handle: (*AuthController).Login},
		{name: "refresh", action: auth.AttemptRefresh, payload: []byte(`{"refresh_token":"rt1.secret"}`), handle: (*AuthController).Refresh},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{decision: auth.AttemptDecision{RetryAfter: 1500 * time.Millisecond}}
			tokens := &authTokenServiceSpy{}
			controller := &AuthController{db: &gorm.DB{}, tokens: tokens, limiter: limiter}
			response := callAuthHandler(controller, test.payload, test.handle)
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAuthError(t, response, "AUTH_RATE_LIMITED")
			if response.Header().Get("Retry-After") != "2" {
				t.Fatalf("Retry-After=%q, want 2", response.Header().Get("Retry-After"))
			}
			if len(limiter.inputs) != 1 || limiter.inputs[0].Action != test.action {
				t.Fatalf("limiter inputs=%+v", limiter.inputs)
			}
			if tokens.issueCalls != 0 || tokens.rotateCalls != 0 {
				t.Fatalf("token service calls: issue=%d rotate=%d", tokens.issueCalls, tokens.rotateCalls)
			}
		})
	}
}

func TestAuthControllerLimiterErrorReturns503(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		handle  func(*AuthController, *gin.Context)
	}{
		{name: "register", payload: []byte(`{"username":"alice","password":"secret123"}`), handle: (*AuthController).Register},
		{name: "login", payload: []byte(`{"username":"alice","password":"secret123"}`), handle: (*AuthController).Login},
		{name: "refresh", payload: []byte(`{"refresh_token":"rt1.secret"}`), handle: (*AuthController).Refresh},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{err: errors.New("Redis unavailable")}
			controller := &AuthController{db: &gorm.DB{}, tokens: &authTokenServiceSpy{}, limiter: limiter}
			response := callAuthHandler(controller, test.payload, test.handle)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAuthError(t, response, "AUTH_RATE_LIMIT_UNAVAILABLE")
		})
	}
}

func TestAuthControllerRejectsOversizedBodyBeforeLimiterAndTokenService(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		handle func(*AuthController, *gin.Context)
	}{
		{name: "register", body: `{"username":"alice","password":"` + strings.Repeat("x", 16<<10) + `"}`, handle: (*AuthController).Register},
		{name: "login", body: `{"username":"alice","password":"` + strings.Repeat("x", 16<<10) + `"}`, handle: (*AuthController).Login},
		{name: "refresh", body: `{"refresh_token":"` + strings.Repeat("x", 16<<10) + `"}`, handle: (*AuthController).Refresh},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
			tokens := &authTokenServiceSpy{}
			controller := &AuthController{db: &gorm.DB{}, tokens: tokens, limiter: limiter}
			response := callAuthHandler(controller, []byte(test.body), test.handle)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAuthError(t, response, "AUTH_REQUEST_TOO_LARGE")
			if len(limiter.inputs) != 0 || tokens.issueCalls != 0 || tokens.rotateCalls != 0 {
				t.Fatalf("expensive operation ran: limiter=%d issue=%d rotate=%d", len(limiter.inputs), tokens.issueCalls, tokens.rotateCalls)
			}
		})
	}
}

func TestAuthControllerInvalidJSONRemains400AndDoesNotCallLimiter(t *testing.T) {
	controller := &AuthController{
		db:      &gorm.DB{},
		tokens:  &authTokenServiceSpy{},
		limiter: &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}},
	}
	response := callAuthHandler(controller, []byte(`{"username":`), (*AuthController).Login)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertAuthError(t, response, "AUTH_REQUEST_INVALID")
	if len(controller.limiter.(*authLimiterSpy).inputs) != 0 {
		t.Fatal("invalid JSON called limiter")
	}
}

func TestAuthControllerRejectsOversizedTrailingBody(t *testing.T) {
	limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
	controller := &AuthController{db: &gorm.DB{}, tokens: &authTokenServiceSpy{}, limiter: limiter}
	payload := append([]byte(`{"username":"alice","password":"secret123"}`), []byte(strings.Repeat(" ", 16<<10))...)
	response := callAuthHandler(controller, payload, (*AuthController).Login)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertAuthError(t, response, "AUTH_REQUEST_TOO_LARGE")
	if len(limiter.inputs) != 0 {
		t.Fatal("oversized trailing body called limiter")
	}
}

func TestAuthControllerNormalizesLoginAndRegisterLimiterSubjects(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   []byte
		handle func(*AuthController, *gin.Context)
	}{
		{name: "register", body: []byte(`{"username":"  Alice ","password":"secret123"}`), handle: (*AuthController).Register},
		{name: "login", body: []byte(`{"username":"  Alice ","password":"secret123"}`), handle: (*AuthController).Login},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{}
			controller := &AuthController{db: &gorm.DB{}, tokens: &authTokenServiceSpy{}, limiter: limiter}
			response := callAuthHandler(controller, test.body, test.handle)
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if len(limiter.inputs) != 1 || limiter.inputs[0].Subject != "alice" {
				t.Fatalf("limiter input=%+v", limiter.inputs)
			}
		})
	}
}

func TestAuthControllerAllowedLimiterContinuesEachAction(t *testing.T) {
	db := newDryRunAuthDB(t)
	for _, test := range []struct {
		name    string
		payload []byte
		handle  func(*AuthController, *gin.Context)
	}{
		{name: "register", payload: []byte(`{"username":"register-user","password":"secret123"}`), handle: (*AuthController).Register},
		{name: "login", payload: []byte(`{"username":"login-user","password":"secret123"}`), handle: (*AuthController).Login},
		{name: "refresh", payload: []byte(`{"refresh_token":"rt1.secret"}`), handle: (*AuthController).Refresh},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
			tokens := &authTokenServiceSpy{}
			controller := &AuthController{db: db, tokens: tokens, limiter: limiter}
			response := callAuthHandler(controller, test.payload, test.handle)
			if response.Code == http.StatusTooManyRequests || len(limiter.inputs) != 1 {
				t.Fatalf("status=%d body=%s limiter=%+v", response.Code, response.Body.String(), limiter.inputs)
			}
		})
	}
}

func callAuthHandler(controller *AuthController, payload []byte, handler func(*AuthController, *gin.Context)) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(controller, ctx)
	return response
}

func assertAuthError(t *testing.T, response *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode auth error: %v; body=%s", err, response.Body.String())
	}
	if body.Code != wantCode {
		t.Fatalf("code=%q, want %q; body=%s", body.Code, wantCode, response.Body.String())
	}
}

func newDryRunAuthDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open("postgres://unused.invalid/unused"), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}
