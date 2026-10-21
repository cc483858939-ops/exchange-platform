package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/auth"
	"Go.exchange/ratelimit"

	"github.com/golang-jwt/jwt/v5"
)

type rateLimitRouteVerifier struct {
	subject string
}

func (v rateLimitRouteVerifier) VerifyAccess(string) (*auth.AccessClaims, error) {
	subject := v.subject
	if subject == "" {
		subject = "7"
	}
	return &auth.AccessClaims{
		SessionID: "rate-limit-test-session",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject,
		},
	}, nil
}

type rateLimitRouteLimiter struct {
	actions []ratelimit.Action
	inputs  []ratelimit.Input
	err     error
}

func (l *rateLimitRouteLimiter) Allow(_ context.Context, input ratelimit.Input) (ratelimit.Decision, error) {
	l.actions = append(l.actions, input.Action)
	l.inputs = append(l.inputs, input)
	if l.err != nil {
		return ratelimit.Decision{}, l.err
	}
	return ratelimit.Decision{
		Allowed:    false,
		Limit:      5,
		Remaining:  0,
		RetryAfter: time.Minute,
		ResetAt:    time.Now().Add(time.Minute),
	}, nil
}

type profileMediaQuotaRouteLimiter struct {
	limit  int64
	counts map[ratelimit.Input]int64
	inputs []ratelimit.Input
}

func (l *profileMediaQuotaRouteLimiter) Allow(_ context.Context, input ratelimit.Input) (ratelimit.Decision, error) {
	l.inputs = append(l.inputs, input)
	if l.counts == nil {
		l.counts = make(map[ratelimit.Input]int64)
	}
	used := l.counts[input]
	decision := ratelimit.Decision{Limit: l.limit, ResetAt: time.Now().Add(time.Minute)}
	if used >= l.limit {
		decision.Remaining = 0
		decision.RetryAfter = time.Minute
		return decision, nil
	}
	l.counts[input] = used + 1
	decision.Allowed = true
	decision.Remaining = l.limit - used - 1
	return decision, nil
}

func TestSetupRouterWiresInitialRateLimitActions(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
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
		{http.MethodPost, "/api/uploads/profile-avatar", ""},
		{http.MethodPost, "/api/uploads/profile-cover", ""},
		{http.MethodPut, "/api/users/8/follow", ""},
		{http.MethodDelete, "/api/users/8/follow", ""},
		{http.MethodPost, "/api/posts/1/translation", `{"target_language":"zh-CN"}`},
		{http.MethodPost, "/api/posts", `{"content":"new post"}`},
		{http.MethodGet, "/api/posts/search?q=posts", ""},
		{http.MethodPut, "/api/posts/1/like", ""},
		{http.MethodDelete, "/api/posts/1/like", ""},
	}
	wantActions := []ratelimit.Action{
		ratelimit.ActionRecommendations,
		ratelimit.ActionMediaUpload,
		ratelimit.ActionProfileMediaUpload,
		ratelimit.ActionProfileMediaUpload,
		ratelimit.ActionFollowMutation,
		ratelimit.ActionFollowMutation,
		ratelimit.ActionTranslation,
		ratelimit.ActionPostCreate,
		ratelimit.ActionPostSearch,
		ratelimit.ActionLikeMutation,
		ratelimit.ActionLikeMutation,
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
		if got := limiter.inputs[index]; got.Subject != "7" || got.Action != wantActions[index] {
			t.Fatalf("after %s %s limiter input=%#v, want subject=7 action=%q", route.method, route.path, got, wantActions[index])
		}
	}
}

func TestProfileMediaUploadsSharePerUserQuota(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &profileMediaQuotaRouteLimiter{limit: 1}
	requests := []struct {
		subject     string
		path        string
		wantLimited bool
	}{
		{subject: "7", path: "/api/uploads/profile-avatar"},
		{subject: "7", path: "/api/uploads/profile-cover", wantLimited: true},
		{subject: "8", path: "/api/uploads/profile-avatar"},
		{subject: "8", path: "/api/uploads/profile-cover", wantLimited: true},
	}
	for _, route := range requests {
		engine, err := SetupRouter(nil, rateLimitRouteVerifier{subject: route.subject}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, route.path, nil)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if route.wantLimited {
			if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"code":"RATE_LIMITED"`) {
				t.Fatalf("POST %s for user %s status=%d body=%s, want a rate-limit denial before upload handling", route.path, route.subject, response.Code, response.Body.String())
			}
			if response.Header().Get("Retry-After") == "" || response.Header().Get("X-RateLimit-Limit") != "1" || response.Header().Get("X-RateLimit-Remaining") != "0" {
				t.Fatalf("POST %s rate-limit headers=%#v", route.path, response.Header())
			}
		} else if response.Code == http.StatusTooManyRequests || strings.Contains(response.Body.String(), `"code":"RATE_LIMITED"`) {
			t.Fatalf("POST %s for user %s was incorrectly charged to another upload quota: status=%d body=%s", route.path, route.subject, response.Code, response.Body.String())
		}
	}

	wantInputs := []ratelimit.Input{
		{Subject: "7", Action: ratelimit.ActionProfileMediaUpload},
		{Subject: "7", Action: ratelimit.ActionProfileMediaUpload},
		{Subject: "8", Action: ratelimit.ActionProfileMediaUpload},
		{Subject: "8", Action: ratelimit.ActionProfileMediaUpload},
	}
	if len(limiter.inputs) != len(wantInputs) {
		t.Fatalf("limiter inputs=%#v, want %#v", limiter.inputs, wantInputs)
	}
	for index, want := range wantInputs {
		if got := limiter.inputs[index]; got != want {
			t.Errorf("limiter input %d=%#v, want %#v", index, got, want)
		}
	}
}

func TestProfileMediaUploadsRequireAuthenticationBeforeRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/uploads/profile-avatar", "/api/uploads/profile-cover"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("POST %s status=%d body=%s, want 401", path, response.Code, response.Body.String())
		}
	}
	if len(limiter.inputs) != 0 {
		t.Fatalf("unauthenticated profile upload reached limiter: %#v", limiter.inputs)
	}
}

func TestProfileMediaUploadsFailClosedWhenLimiterUnavailable(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	for _, test := range []struct {
		name    string
		limiter ratelimit.Limiter
	}{
		{name: "nil limiter"},
		{name: "limiter error", limiter: &rateLimitRouteLimiter{err: errors.New("redis unavailable")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, test.limiter, newRouterRecommendationHandler(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/uploads/profile-avatar", "/api/uploads/profile-cover"} {
				request := httptest.NewRequest(http.MethodPost, path, nil)
				request.Header.Set("Authorization", "Bearer test-token")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"RATE_LIMIT_UNAVAILABLE"`) {
					t.Errorf("POST %s status=%d body=%s, want fail-closed 503", path, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestSetupRouterExplicitlyDisablesApplicationRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, nil, newRouterRecommendationHandler(t), nil)
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

func TestLikeMutationFailsClosedWhenLimiterIsUnavailable(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		request := httptest.NewRequest(method, "/api/posts/7/like", nil)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"RATE_LIMIT_UNAVAILABLE"`) {
			t.Fatalf("%s Like without limiter status=%d body=%s, want fail-closed 503", method, response.Code, response.Body.String())
		}
	}
}

func TestLikeMutationRequiresAuthenticationBeforeRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/posts/7/like", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || len(limiter.actions) != 0 {
		t.Fatalf("unauthenticated Like status=%d actions=%#v body=%s", response.Code, limiter.actions, response.Body.String())
	}
}

func TestLikeReadDoesNotUseMutationRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/posts/7/like", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code == http.StatusTooManyRequests || response.Code == http.StatusServiceUnavailable || len(limiter.actions) != 0 {
		t.Fatalf("GET Like unexpectedly used mutation limiter status=%d actions=%#v body=%s", response.Code, limiter.actions, response.Body.String())
	}
}

func TestPostSearchRequiresAuthenticationBeforeRateLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	limiter := &rateLimitRouteLimiter{}
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, limiter, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/posts/search?q=posts", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated Post search status=%d body=%s, want 401", response.Code, response.Body.String())
	}
	if len(limiter.actions) != 0 {
		t.Fatalf("unauthenticated Post search reached rate limiter: %#v", limiter.actions)
	}
}

func TestPostDetailRemainsPublicBesideAuthenticatedSearch(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/posts/42", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("public Post detail unexpectedly requires authentication: %s", response.Body.String())
	}
}

func TestSetupRouterRequiresRecommendationHandler(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	if _, err := SetupRouter(nil, rateLimitRouteVerifier{}, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("SetupRouter unexpectedly accepted a missing recommendation handler")
	}
}

func TestSetupRouterExposesRateLimitHeadersThroughCORS(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
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
	for _, header := range []string{"Retry-After", "X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if !strings.Contains(strings.ToLower(exposed), strings.ToLower(header)) {
			t.Fatalf("exposed headers=%q missing %q", exposed, header)
		}
	}
}
