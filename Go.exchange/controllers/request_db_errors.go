package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleRequestDBError suppresses writes after a client disconnect and maps
// request or PostgreSQL query/lock timeouts to Gateway Timeout.
func handleRequestDBError(ctx *gin.Context, err error) bool {
	if err == nil || ctx == nil || ctx.Request == nil {
		return false
	}

	requestErr := ctx.Request.Context().Err()
	if errors.Is(requestErr, context.Canceled) {
		ctx.Abort()
		return true
	}
	if errors.Is(requestErr, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		isPostgresTimeoutError(err) {
		if !ctx.Writer.Written() {
			ctx.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{"error": "request timed out"})
		} else {
			ctx.Abort()
		}
		return true
	}
	return false
}

func isPostgresTimeoutError(err error) bool {
	var stateError interface{ SQLState() string }
	if !errors.As(err, &stateError) {
		return false
	}
	switch stateError.SQLState() {
	case "57014", // query_canceled / statement_timeout
		"55P03": // lock_not_available / lock_timeout
		return true
	default:
		return false
	}
}
