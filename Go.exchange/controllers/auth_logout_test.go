package controllers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/auth"
	"github.com/gin-gonic/gin"
)

func (*authTokenServiceSpy) RevokeRefresh(context.Context, string) error { return nil }
func (stubTokenService) RevokeRefresh(context.Context, string) error     { return nil }
func (refreshTokenService) RevokeRefresh(context.Context, string) error  { return nil }

type logoutTokenSpy struct {
	authTokenServiceSpy
	credentials []string
	err         error
}

func (s *logoutTokenSpy) RevokeRefresh(_ context.Context, credential string) error {
	s.credentials = append(s.credentials, credential)
	return s.err
}

func callLogoutHandler(controller *AuthController, payload []byte) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/auth/logout", controller.Logout)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestLogoutHandlerUsesCapturedCredentialWithoutAccessOrUserLookup(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"success", nil, 204, ""},
		{"invalid credential", auth.ErrRefreshInvalid, 401, "AUTH_REFRESH_INVALID"},
		{"Redis unavailable", errors.New("private storage failure"), 503, "AUTH_LOGOUT_UNAVAILABLE"},
		{"cancelled", context.Canceled, 503, "AUTH_LOGOUT_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := &logoutTokenSpy{err: test.err}
			limiter := &authLimiterSpy{decision: auth.AttemptDecision{Allowed: true}}
			// No DB is supplied: logout must not depend on profile availability.
			controller := &AuthController{tokens: tokens, limiter: limiter}
			response := callLogoutHandler(controller, []byte(`{"refresh_token":"captured-old-refresh"}`))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if len(tokens.credentials) != 1 || tokens.credentials[0] != "captured-old-refresh" || tokens.issueCalls != 0 || tokens.rotateCalls != 0 {
				t.Fatal("incorrect credential or replacement issued")
			}
			if len(limiter.inputs) != 1 || limiter.inputs[0].Action != auth.AttemptLogout {
				t.Fatal("logout rate limit missing")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("logout response cacheable")
			}
			if test.code != "" {
				assertAuthError(t, response, test.code)
			}
			if strings.Contains(response.Body.String(), "private storage") {
				t.Fatal("storage error exposed")
			}
		})
	}
}

func TestLogoutMalformedOversizedAndRateLimitedRequestsDoNotRevoke(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		limit      auth.AttemptDecision
		limiterErr error
	}{
		{"missing", `{}`, 400, auth.AttemptDecision{Allowed: true}, nil},
		{"sid only", `{"session_id":"other-session"}`, 400, auth.AttemptDecision{Allowed: true}, nil},
		{"trailing", `{"refresh_token":"x"} {}`, 400, auth.AttemptDecision{Allowed: true}, nil},
		{"oversized", `{"refresh_token":"` + strings.Repeat("x", 16<<10) + `"}`, 413, auth.AttemptDecision{Allowed: true}, nil},
		{"rate limited", `{"refresh_token":"x"}`, 429, auth.AttemptDecision{}, nil},
		{"limiter unavailable", `{"refresh_token":"x"}`, 503, auth.AttemptDecision{}, errors.New("unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := &logoutTokenSpy{}
			controller := &AuthController{tokens: tokens, limiter: &authLimiterSpy{decision: test.limit, err: test.limiterErr}}
			response := callLogoutHandler(controller, []byte(test.body))
			if response.Code != test.status || len(tokens.credentials) != 0 {
				t.Fatalf("status=%d revocations=%d", response.Code, len(tokens.credentials))
			}
		})
	}
}

var _ auth.TokenService = (*logoutTokenSpy)(nil)
