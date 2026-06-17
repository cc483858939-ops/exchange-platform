package controllers

import (
	"errors"
	"sort"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ArticleBehaviorActionView = "view"
	ArticleBehaviorActionLike = "like"

	articleBehaviorRetentionLimit = 200
)

var recordArticleBehavior = func(userID uint, articleID uint, action string) error {
	action = strings.TrimSpace(action)
	if userID == 0 || articleID == 0 || action == "" {
		return nil
	}
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	now := time.Now()
	behavior := models.ArticleBehavior{
		UserID:     userID,
		ArticleID:  articleID,
		Action:     action,
		Count:      1,
		LastSeenAt: now,
		Active:     true,
	}

	if err := global.Db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "article_id"},
			{Name: "action"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":        gorm.Expr("count + ?", 1),
			"last_seen_at": now,
			"active":       true,
			"updated_at":   now,
		}),
	}).Create(&behavior).Error; err != nil {
		return err
	}

	return enforceArticleBehaviorRetention(userID, action)
}

var loadActiveArticleBehaviorsForRetention = func(userID uint, action string) ([]models.ArticleBehavior, error) {
	var behaviors []models.ArticleBehavior
	err := global.Db.
		Select("id,last_seen_at,active").
		Where("user_id = ? AND action = ? AND active = ?", userID, action, true).
		Find(&behaviors).Error
	return behaviors, err
}

var archiveArticleBehaviorIDs = func(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return global.Db.Model(&models.ArticleBehavior{}).
		Where("id IN ?", ids).
		Update("active", false).Error
}

var archiveArticleBehavior = func(userID uint, articleID uint, action string) error {
	action = strings.TrimSpace(action)
	if userID == 0 || articleID == 0 || action == "" {
		return nil
	}
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return global.Db.Model(&models.ArticleBehavior{}).
		Where("user_id = ? AND article_id = ? AND action = ? AND active = ?", userID, articleID, action, true).
		Update("active", false).Error
}

var enforceArticleBehaviorRetention = func(userID uint, action string) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	behaviors, err := loadActiveArticleBehaviorsForRetention(userID, action)
	if err != nil {
		return err
	}

	return archiveArticleBehaviorIDs(articleBehaviorIDsBeyondRetention(behaviors, articleBehaviorRetentionLimit))
}

func articleBehaviorIDsBeyondRetention(behaviors []models.ArticleBehavior, limit int) []uint {
	if limit <= 0 {
		limit = articleBehaviorRetentionLimit
	}

	activeBehaviors := make([]models.ArticleBehavior, 0, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ID == 0 || !behavior.Active {
			continue
		}
		activeBehaviors = append(activeBehaviors, behavior)
	}

	sort.SliceStable(activeBehaviors, func(i, j int) bool {
		left := activeBehaviors[i]
		right := activeBehaviors[j]
		if !left.LastSeenAt.Equal(right.LastSeenAt) {
			return left.LastSeenAt.After(right.LastSeenAt)
		}
		return left.ID > right.ID
	}) //按照上次查看时间排序

	if len(activeBehaviors) <= limit {
		return nil
	}

	archiveIDs := make([]uint, 0, len(activeBehaviors)-limit)
	for _, behavior := range activeBehaviors[limit:] {
		archiveIDs = append(archiveIDs, behavior.ID)
	}
	return archiveIDs
} //得到超过限度的对应文章，把他的active设为false

var articleBehaviorLogError = func(ctx *gin.Context, msg string, err error) {
	if global.Db != nil {
		global.Db.Logger.Error(ctx, msg, err)
	}
}

func recordArticleBehaviorFromContext(ctx *gin.Context, articleID uint, action string) {
	userID, ok := userIDFromContext(ctx) //从ctx中提取username
	if !ok {
		return
	}

	if err := recordArticleBehavior(userID, articleID, action); err != nil { //记录行为
		articleBehaviorLogError(ctx, "failed to record article behavior", err)
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
