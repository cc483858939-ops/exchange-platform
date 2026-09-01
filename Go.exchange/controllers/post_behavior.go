package controllers

import "github.com/gin-gonic/gin"

const (
	PostBehaviorActionView  = "view"
	PostBehaviorActionReply = "reply"
)

func userIDFromContext(ctx *gin.Context) (uint, bool) {
	if ctx == nil {
		return 0, false
	}
	value, exists := ctx.Get("user_id")
	if !exists {
		return 0, false
	}
	switch userID := value.(type) {
	case uint:
		return userID, userID > 0
	case uint64:
		return uint(userID), userID > 0
	case int:
		return uint(userID), userID > 0
	case float64:
		id := uint(userID)
		return id, id > 0 && userID == float64(id)
	default:
		return 0, false
	}
}
