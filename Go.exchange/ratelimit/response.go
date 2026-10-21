package ratelimit

import (
	"Go.exchange/apierrors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

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

func writeRateLimitResponse(ctx *gin.Context, code apierrors.Code) {
	apierrors.WritePublicError(ctx, code)
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
