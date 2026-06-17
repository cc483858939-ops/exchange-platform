package middlewares

import (
	"Go.exchange/utils"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func AuthMiddleWare() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		token := ctx.GetHeader("Authorization")
		if token == "" {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization Header"})
			ctx.Abort()
			return
		}
		_, claims, err := utils.ParseJWT(token)
		if err != nil {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			ctx.Abort()
			return
		}
		if tokenType, ok := claims["type"].(string); !ok || tokenType != "access" {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Please use access token"})
			ctx.Abort()
			return
		}

		username, ok := claims["username"].(string)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid claims"})
			ctx.Abort()
			return
		}
		userID, ok := jwtUserIDClaim(claims["user_id"])
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid claims"})
			ctx.Abort()
			return
		}

		ctx.Set("user_id", userID)
		ctx.Set("username", username)
		ctx.Next()
	}
}

func jwtUserIDClaim(value interface{}) (uint, bool) {
	switch userID := value.(type) {
	case float64:
		id := uint(userID)
		return id, id > 0 && userID == float64(id)
	case uint:
		return userID, userID > 0
	case int:
		return uint(userID), userID > 0
	case string:
		parsed, err := strconv.ParseUint(userID, 10, 64)
		if err != nil || parsed == 0 {
			return 0, false
		}
		return uint(parsed), true
	default:
		return 0, false
	}
}
