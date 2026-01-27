package middlewares

import (
	"aceld/utils"
	"net/http"

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

		ctx.Set("username", username)
		ctx.Next()
	}
}
