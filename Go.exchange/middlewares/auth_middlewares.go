package middlewares

import (
	"Go.exchange/apierrors"
	"Go.exchange/auth"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware(verifier auth.AccessTokenVerifier) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		rawToken, ok := bearerToken(ctx.GetHeader("Authorization"))
		if !ok {
			apierrors.WritePublicError(ctx, apierrors.CodeAuthTokenMissing)
			return
		}

		claims, err := verifier.VerifyAccess(rawToken)
		if err != nil {
			apierrors.WritePublicError(ctx, apierrors.CodeAuthTokenInvalid)
			return
		}
		userID, err := strconv.ParseUint(claims.Subject, 10, 64)
		if err != nil || userID == 0 || uint64(uint(userID)) != userID {
			apierrors.WritePublicError(ctx, apierrors.CodeAuthTokenInvalid)
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
