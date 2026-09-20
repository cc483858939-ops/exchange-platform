package ratelimit

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const AuthenticatedUserIDKey = "user_id"

type FailureMode int

const (
	FailOpen FailureMode = iota
	FailClosed
)

func Middleware(limiter Limiter, action Action, failureMode FailureMode) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if Enforce(ctx, limiter, action, failureMode) {
			ctx.Next()
		}
	}
}

// Enforce applies a rate limit inside a handler that needs to place the check
// after request-specific replay/idempotency logic. It returns true only when
// the caller should continue processing the request.
func Enforce(ctx *gin.Context, limiter Limiter, action Action, failureMode FailureMode) bool {
	if ctx == nil {
		return false
	}
	subject, ok := SubjectFromContext(ctx)
	if !ok {
		writeRateLimitResponse(ctx, http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication required")
		return false
	}
	if limiter == nil {
		return handleLimiterError(ctx, action, failureMode, fmt.Errorf("rate limiter is unavailable"))
	}
	requestContext := context.Background()
	if ctx.Request != nil {
		requestContext = ctx.Request.Context()
	}
	decision, err := limiter.Allow(requestContext, Input{Subject: subject, Action: action})
	if err != nil {
		return handleLimiterError(ctx, action, failureMode, err)
	}
	setRateLimitHeaders(ctx, decision)
	if !decision.Allowed {
		writeRateLimitResponse(ctx, http.StatusTooManyRequests, rateLimitedCode, "Too many requests")
		return false
	}
	return true
}

func SubjectFromContext(ctx *gin.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Get(AuthenticatedUserIDKey)
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case uint:
		if typed > 0 {
			return strconv.FormatUint(uint64(typed), 10), true
		}
	case uint64:
		if typed > 0 && uint64(uint(typed)) == typed {
			return strconv.FormatUint(typed, 10), true
		}
	case int:
		if typed > 0 {
			return strconv.Itoa(typed), true
		}
	case int64:
		if typed > 0 && uint64(typed) <= uint64(^uint(0)) {
			return strconv.FormatInt(typed, 10), true
		}
	}
	return "", false
}

func handleLimiterError(ctx *gin.Context, action Action, failureMode FailureMode, err error) bool {
	log.Printf("[RateLimit] action=%s failure_mode=%s error=%v", action, failureModeName(failureMode), err)
	if failureMode == FailOpen {
		return true
	}
	writeRateLimitResponse(ctx, http.StatusServiceUnavailable, rateLimitUnavailableCode, "Rate limiting is temporarily unavailable")
	return false
}

func failureModeName(mode FailureMode) string {
	if mode == FailOpen {
		return "fail_open"
	}
	return "fail_closed"
}
