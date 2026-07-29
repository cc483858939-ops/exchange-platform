package controllers

import (
	"Go.exchange/eventing"
	"Go.exchange/global"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

const (
	ArticleBehaviorActionView = "view"
	ArticleBehaviorActionLike = "like"
)

var recordArticleBehavior = func(userID uint, articleID uint, action string) error {
	action = strings.TrimSpace(action)
	if userID == 0 || articleID == 0 || action == "" {
		return nil
	}
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	event, err := eventing.NewUserBehavior(userID, articleID, action, "api")
	if err != nil {
		return err
	}
	return global.Db.Transaction(func(tx *gorm.DB) error { return eventing.AddOutboxEvent(tx, event) })
}
var articleBehaviorLogError = func(ctx *gin.Context, msg string, err error) {
	if global.Db != nil {
		global.Db.Logger.Error(ctx, msg, err)
	}
}

func recordArticleBehaviorFromContext(ctx *gin.Context, articleID uint, action string) {
	userID, ok := userIDFromContext(ctx)
	if ok {
		if err := recordArticleBehavior(userID, articleID, action); err != nil {
			articleBehaviorLogError(ctx, "failed to record article behavior", err)
		}
	}
}
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
