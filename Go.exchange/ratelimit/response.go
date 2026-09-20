package ratelimit

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	rateLimitedCode          = "RATE_LIMITED"
	rateLimitUnavailableCode = "RATE_LIMIT_UNAVAILABLE"
)

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func setRateLimitHeaders(ctx *gin.Context, decision Decision) {
	if ctx == nil {
		return
	}
	ctx.Header("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
	ctx.Header("X-RateLimit-Remaining", strconv.FormatInt(maxInt64(decision.Remaining, 0), 10))
	if !decision.ResetAt.IsZero() {
		ctx.Header("X-RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
	}
	if !decision.Allowed {
		ctx.Header("Retry-After", strconv.FormatInt(retryAfterSeconds(decision.RetryAfter), 10))
	}
}

func writeRateLimitResponse(ctx *gin.Context, status int, code, message string) {
	ctx.AbortWithStatusJSON(status, errorResponse{Code: code, Message: message})
}

func retryAfterSeconds(duration time.Duration) int64 {
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return int64(seconds)
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}
