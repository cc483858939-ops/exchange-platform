package middlewares

import (
	"errors"
	"strconv"
	"strings"

	"Go.exchange/apierrors"
	"Go.exchange/auth"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware(verifier auth.AccessTokenVerifier) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		rawToken, ok := bearerToken(ctx.GetHeader("Authorization"))
		if !ok {
			apierrors.WritePublicError(ctx, apierrors.CodeAuthTokenMissing)
			return
		}

		var claims *auth.AccessClaims
		var err error
		if contextual, ok := verifier.(auth.ContextAccessTokenVerifier); ok {
			claims, err = contextual.VerifyAccessContext(ctx.Request.Context(), rawToken)
		} else {
			claims, err = verifier.VerifyAccess(rawToken)
		}
		if err != nil {
			if errors.Is(err, auth.ErrAuthVersionUnavailable) {
				apierrors.WriteInternalErrorAs(ctx, err, apierrors.CodeAuthVersionUnavailable)
				return
			}
			apierrors.WritePublicError(ctx, apierrors.CodeAuthTokenInvalid)
			return
		}
		if claims == nil {
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
