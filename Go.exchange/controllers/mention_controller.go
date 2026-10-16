package controllers

import (
	"net/http"
	"strconv"
	"strings"

	"Go.exchange/global"
	"Go.exchange/postmention"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultMentionSuggestionLimit = 8
	maxMentionSuggestionLimit     = 8
	maxMentionPrefixRunes         = postmention.MaxUsernameRunes
)

type mentionSuggestionUser struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type mentionSuggestionItem struct {
	User   mentionSuggestionUser `json:"user"`
	Reason string                `json:"reason"`
}

type mentionSuggestionRow struct {
	ID          uint   `gorm:"column:id"`
	Username    string `gorm:"column:username"`
	DisplayName string `gorm:"column:display_name"`
	AvatarURL   string `gorm:"column:avatar_url"`
}

func GetMyMentionSuggestions(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	prefix, ok := postmention.ValidPrefix(ctx.Query("prefix"))
	if !ok || len([]rune(prefix)) > maxMentionPrefixRunes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid mention prefix"})
		return
	}
	limit := defaultMentionSuggestionLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxMentionSuggestionLimit {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid mention suggestion limit"})
			return
		}
		limit = parsed
	}
	if global.APIDb == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	db := global.APIDb.WithContext(ctx.Request.Context())
	items, err := loadMentionSuggestions(db, viewerID, prefix, limit)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": items})
}

func loadMentionSuggestions(db *gorm.DB, viewerID uint, prefix string, limit int) ([]mentionSuggestionItem, error) {
	if db == nil || viewerID == 0 || limit < 1 || limit > maxMentionSuggestionLimit {
		return nil, gorm.ErrInvalidData
	}
	pattern := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(prefix)
	query := func(q *gorm.DB) *gorm.DB {
		if prefix != "" {
			q = q.Where(`LOWER(u.username) LIKE LOWER(?) ESCAPE '\'`, pattern+"%")
		}
		return q
	}
	items := make([]mentionSuggestionItem, 0, limit)
	recentLimit := limit
	if recentLimit > 4 {
		recentLimit = 4
	}
	var recent []mentionSuggestionRow
	recentQuery := db.Table("user_mention_recents AS r").
		Select("u.id, u.username, u.display_name, u.avatar_url").
		Joins("JOIN users AS u ON u.id = r.mentioned_user_id AND u.deleted_at IS NULL").
		Where("r.user_id = ?", viewerID).
		Where("NOT EXISTS (SELECT 1 FROM user_mutes AS um WHERE um.muter_id = ? AND um.muted_id = u.id)", viewerID)
	if err := query(recentQuery).Order("r.last_mentioned_at DESC, r.mentioned_user_id DESC").Limit(recentLimit).Scan(&recent).Error; err != nil {
		return nil, err
	}
	for _, row := range recent {
		items = append(items, mentionSuggestionItem{User: mentionSuggestionUser{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL}, Reason: "recently_mentioned"})
	}
	remaining := limit - len(items)
	if remaining == 0 {
		return items, nil
	}
	followingQuery := db.Table("user_follows AS f").
		Select("u.id, u.username, u.display_name, u.avatar_url").
		Joins("JOIN users AS u ON u.id = f.following_id AND u.deleted_at IS NULL").
		Where("f.follower_id = ? AND f.following_id <> ?", viewerID, viewerID).
		Where("NOT EXISTS (SELECT 1 FROM user_mutes AS um WHERE um.muter_id = ? AND um.muted_id = u.id)", viewerID).
		Where("NOT EXISTS (SELECT 1 FROM user_mention_recents AS r WHERE r.user_id = ? AND r.mentioned_user_id = u.id)", viewerID)
	var following []mentionSuggestionRow
	if err := query(followingQuery).Order("f.created_at DESC, f.following_id DESC").Limit(remaining).Scan(&following).Error; err != nil {
		return nil, err
	}
	for _, row := range following {
		items = append(items, mentionSuggestionItem{User: mentionSuggestionUser{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL}, Reason: "following"})
	}
	return items, nil
}
