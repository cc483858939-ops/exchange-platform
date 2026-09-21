package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/auth"
	"Go.exchange/ratelimit"

	"github.com/golang-jwt/jwt/v5"
)

type rateLimitRouteVerifier struct{}

func (rateLimitRouteVerifier) VerifyAccess(string) (*auth.AccessClaims, error) {
	return &auth.AccessClaims{
		SessionID: "rate-limit-test-session",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "7",
		},
	}, nil
}

type rateLimitRouteLimiter struct {
	actions []ratelimit.Action
}

func (l *rateLimitRouteLimiter) Allow(_ context.Context, input ratelimit.Input) (ratelimit.Decision, error) {
	l.actions = append(l.actions, input.Action)
	return ratelimit.Decision{
		Allowed:    false,
		Limit:      5,
		Remaining:  0,
		RetryAfter: time.Minute,
		ResetAt:    time.Now().Add(time.Minute),
	}, nil
}

func TestSetupRouterWiresInitialRateLimitActions(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouterWithRateLimiter(nil, rateLimitRouteVerifier{}, nil, nil, limiter)
	if err != nil {
		t.Fatal(err)
	}

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/recommendations/posts", ""},
		{http.MethodPost, "/api/uploads/post-media", ""},
		{http.MethodPut, "/api/users/8/follow", ""},
		{http.MethodDelete, "/api/users/8/follow", ""},
		{http.MethodPost, "/api/posts/1/translation", `{"target_language":"zh-CN"}`},
		{http.MethodPost, "/api/posts", `{"content":"new post"}`},
	}
	wantActions := []ratelimit.Action{
		ratelimit.ActionRecommendations,
		ratelimit.ActionMediaUpload,
		ratelimit.ActionFollowMutation,
		ratelimit.ActionFollowMutation,
		ratelimit.ActionTranslation,
		ratelimit.ActionPostCreate,
	}
	for index, route := range requests {
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		if route.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("%s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
		if len(limiter.actions) != index+1 || limiter.actions[index] != wantActions[index] {
			t.Fatalf("after %s %s actions=%#v, want latest %q", route.method, route.path, limiter.actions, wantActions[index])
		}
	}
}

func TestSetupRouterExplicitlyDisablesApplicationRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/users/7/follow", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code == http.StatusServiceUnavailable && strings.Contains(response.Body.String(), `"code":"RATE_LIMIT_UNAVAILABLE"`) {
		t.Fatalf("legacy router unexpectedly applied application rate limiting: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSetupRouterWithRateLimiterNilDependencyFailsClosed(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouterWithRateLimiter(nil, rateLimitRouteVerifier{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/users/7/follow", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want 503", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"RATE_LIMIT_UNAVAILABLE"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestSetupRouterExposesRateLimitHeadersThroughCORS(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	engine, err := SetupRouter(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "https://app.example.test")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	exposed := response.Header().Get("Access-Control-Expose-Headers")
	for _, header := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if !strings.Contains(strings.ToLower(exposed), strings.ToLower(header)) {
			t.Fatalf("exposed headers=%q missing %q", exposed, header)
		}
	}
}
