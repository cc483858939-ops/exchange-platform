package middlewares

import (
	"Go.exchange/auth"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware(verifier auth.AccessTokenVerifier) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		rawToken, ok := bearerToken(ctx.GetHeader("Authorization"))
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{
				"code":    "AUTH_TOKEN_MISSING",
				"message": "Authentication required",
			})
			ctx.Abort()
			return
		}

		claims, err := verifier.VerifyAccess(rawToken)
		if err != nil {
			ctx.JSON(http.StatusUnauthorized, gin.H{
				"code":    "AUTH_TOKEN_INVALID",
				"message": "Authentication failed",
			})
			ctx.Abort()
			return
		}
		userID, err := strconv.ParseUint(claims.Subject, 10, 64)
		if err != nil || userID == 0 || uint64(uint(userID)) != userID {
			ctx.JSON(http.StatusUnauthorized, gin.H{
				"code":    "AUTH_TOKEN_INVALID",
				"message": "Authentication failed",
			})
			ctx.Abort()
			return
		}

		ctx.Set("user_id", uint(userID))
		ctx.Set("session_id", claims.SessionID)
		ctx.Next()
	}
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
