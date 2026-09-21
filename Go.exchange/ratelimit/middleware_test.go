package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type fakeLimiter struct {
	decision Decision
	err      error
	inputs   []Input
}

func (f *fakeLimiter) Allow(_ context.Context, input Input) (Decision, error) {
	f.inputs = append(f.inputs, input)
	return f.decision, f.err
}

func TestMiddlewareAllowsAndSetsHeaders(t *testing.T) {
	limiter := &fakeLimiter{decision: Decision{
		Allowed: true, Limit: 5, Remaining: 4, ResetAt: time.Unix(1_700_000_060, 0).UTC(),
	}}
	called := false
	engine := rateLimitTestRouter(limiter, ActionPostCreate, FailClosed, &called)
	response := serveRateLimitRequest(engine)

	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
	if response.Header().Get("X-RateLimit-Limit") != "5" || response.Header().Get("X-RateLimit-Remaining") != "4" || response.Header().Get("X-RateLimit-Reset") != "1700000060" {
		t.Fatalf("unexpected headers: %#v", response.Header())
	}
	if response.Header().Get("Retry-After") != "" {
		t.Fatalf("allowed response unexpectedly had Retry-After: %q", response.Header().Get("Retry-After"))
	}
	if len(limiter.inputs) != 1 || limiter.inputs[0] != (Input{Subject: "7", Action: ActionPostCreate}) {
		t.Fatalf("unexpected limiter input: %#v", limiter.inputs)
	}
}

func TestMiddlewareDeniesWithoutCallingDownstream(t *testing.T) {
	limiter := &fakeLimiter{decision: Decision{
		Allowed: false, Limit: 5, Remaining: 0, RetryAfter: 3 * time.Second, ResetAt: time.Unix(1_700_000_060, 0).UTC(),
	}}
	called := false
	engine := rateLimitTestRouter(limiter, ActionPostCreate, FailClosed, &called)
	response := serveRateLimitRequest(engine)

	if response.Code != http.StatusTooManyRequests || called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
	if response.Header().Get("Retry-After") != "3" || response.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("unexpected denial headers: %#v", response.Header())
	}
	if !strings.Contains(response.Body.String(), `"code":"RATE_LIMITED"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestMiddlewareFailureModes(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       FailureMode
		wantStatus int
		wantCalled bool
		wantCode   string
	}{
		{name: "fail open", mode: FailOpen, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "fail closed", mode: FailClosed, wantStatus: http.StatusServiceUnavailable, wantCode: "RATE_LIMIT_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &fakeLimiter{err: errors.New("redis unavailable")}
			called := false
			engine := rateLimitTestRouter(limiter, ActionTranslation, test.mode, &called)
			response := serveRateLimitRequest(engine)
			if response.Code != test.wantStatus || called != test.wantCalled {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
			if test.wantCode != "" && !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestMiddlewareNilLimiterFailureModes(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       FailureMode
		wantStatus int
		wantCalled bool
	}{
		{name: "fail open", mode: FailOpen, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "fail closed", mode: FailClosed, wantStatus: http.StatusServiceUnavailable, wantCalled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			engine := rateLimitTestRouter(nil, ActionTranslation, test.mode, &called)
			response := serveRateLimitRequest(engine)
			if response.Code != test.wantStatus || called != test.wantCalled {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
			if test.mode == FailClosed && !strings.Contains(response.Body.String(), `"code":"RATE_LIMIT_UNAVAILABLE"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestMiddlewareRequiresAuthenticatedSubject(t *testing.T) {
	limiter := &fakeLimiter{decision: Decision{Allowed: true}}
	called := false
	engine := gin.New()
	engine.Use(Middleware(limiter, ActionPostCreate, FailClosed))
	engine.GET("/test", func(ctx *gin.Context) {
		called = true
		ctx.Status(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	if response.Code != http.StatusUnauthorized || called || len(limiter.inputs) != 0 {
		t.Fatalf("status=%d called=%v inputs=%#v body=%s", response.Code, called, limiter.inputs, response.Body.String())
	}
}

func rateLimitTestRouter(limiter Limiter, action Action, mode FailureMode, called *bool) *gin.Engine {
	engine := gin.New()
	engine.Use(func(ctx *gin.Context) {
		ctx.Set(AuthenticatedUserIDKey, uint(7))
		ctx.Next()
	})
	engine.Use(Middleware(limiter, action, mode))
	engine.GET("/test", func(ctx *gin.Context) {
		*called = true
		ctx.Status(http.StatusNoContent)
	})
	return engine
}

func serveRateLimitRequest(engine *gin.Engine) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	return response
}
