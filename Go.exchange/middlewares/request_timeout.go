package middlewares

import (
	"context"
	"net/http"
	"strings"

	"Go.exchange/config"

	"github.com/gin-gonic/gin"
)

// RequestTimeout gives ordinary API requests and media uploads separate
// budgets while preserving any earlier deadline on the parent request.
func RequestTimeout() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		budget := config.APIRequestTimeout()
		if strings.HasPrefix(ctx.Request.URL.Path, "/api/uploads/") {
			budget = config.APIUploadRequestTimeout()
		}

		requestCtx, cancel := context.WithTimeout(ctx.Request.Context(), budget)
		defer cancel()
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Next()

		if requestCtx.Err() == context.DeadlineExceeded && !ctx.Writer.Written() {
			ctx.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{"error": "request timed out"})
		}
	}
}
