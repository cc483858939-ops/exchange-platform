package ratelimit

import (
	"context"
	"errors"
	"strconv"

	"Go.exchange/apierrors"
	"Go.exchange/metrics"

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
		writeRateLimitResponse(ctx, apierrors.CodeAuthRequired)
		return false
	}
	if limiter == nil {
		metrics.RecordRateLimitError(string(action))
		return handleLimiterError(ctx, failureMode, errors.New("rate limiter is unavailable"))
	}
	requestContext := context.Background()
	if ctx.Request != nil {
		requestContext = ctx.Request.Context()
	}
	decision, err := limiter.Allow(requestContext, Input{Subject: subject, Action: action})
	if err != nil {
		return handleLimiterError(ctx, failureMode, err)
	}
	setRateLimitHeaders(ctx, decision)
	if !decision.Allowed {
		writeRateLimitResponse(ctx, apierrors.CodeRateLimited)
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

func handleLimiterError(ctx *gin.Context, failureMode FailureMode, err error) bool {
	if failureMode == FailOpen {
		apierrors.LogInternalError(ctx, 0, apierrors.CodeRateLimitUnavailable, err)
		return true
	}
	apierrors.WriteInternalErrorAs(ctx, err, apierrors.CodeRateLimitUnavailable)
	return false
}
