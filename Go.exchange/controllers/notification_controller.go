package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultNotificationLimit  = 20
	maxNotificationLimit      = 50
	notificationCursorVersion = 1
)

type notificationCursor struct {
	Version    int       `json:"v"`
	ActivityAt time.Time `json:"activity_at"`
	ID         uint      `json:"id"`
}

type notificationActorResponse struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type notificationResponse struct {
	ID         uint                      `json:"id"`
	Type       string                    `json:"type"`
	Actor      notificationActorResponse `json:"actor"`
	ArticleID  *uint                     `json:"article_id"`
	CommentID  *uint                     `json:"comment_id"`
	ActivityAt time.Time                 `json:"activity_at"`
	Read       bool                      `json:"read"`
}

type notificationPageResponse struct {
	Items      []notificationResponse `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type notificationQueryRow struct {
	ID          uint       `gorm:"column:id"`
	Type        string     `gorm:"column:notification_type"`
	ActorID     uint       `gorm:"column:actor_id"`
	Username    string     `gorm:"column:username"`
	DisplayName string     `gorm:"column:display_name"`
	AvatarURL   string     `gorm:"column:avatar_url"`
	ArticleID   *uint      `gorm:"column:article_id"`
	CommentID   *uint      `gorm:"column:comment_id"`
	ActivityAt  time.Time  `gorm:"column:activity_at"`
	ReadAt      *time.Time `gorm:"column:read_at"`
}

// visibleNotificationsForViewer is the single visibility query shared by the
// list and unread-count endpoints. Filtering happens before ordering/limit.
func visibleNotificationsForViewer(db *gorm.DB, viewerID uint, now time.Time) *gorm.DB {
	if db == nil {
		return nil
	}
	visibleArticles := publicArticleScope(db.Table("articles"), now).Select("articles.id")
	return db.Table("notifications AS n").
		Select(`n.id, n.notification_type, n.actor_id, actor.username, actor.display_name, actor.avatar_url,
                n.article_id, n.comment_id, n.activity_at, n.read_at`).
		Joins("JOIN users AS actor ON actor.id = n.actor_id AND actor.deleted_at IS NULL").
		Joins("LEFT JOIN (?) AS visible_article ON visible_article.id = n.article_id", visibleArticles).
		Joins("LEFT JOIN comments AS visible_comment ON visible_comment.id = n.comment_id AND visible_comment.deleted_at IS NULL AND visible_comment.article_id = n.article_id").
		Where("n.recipient_id = ?", viewerID).
		Where(`
(
  n.notification_type = ? AND n.article_id IS NULL AND n.comment_id IS NULL
) OR (
  n.notification_type = ? AND visible_article.id IS NOT NULL AND n.comment_id IS NULL
) OR (
  n.notification_type = ? AND visible_article.id IS NOT NULL AND visible_comment.id IS NOT NULL
)`, models.NotificationTypeUserFollowed, models.NotificationTypePostLiked, models.NotificationTypePostReplied)
}

func parseNotificationQuery(ctx *gin.Context) (int, *notificationCursor, error) {
	limit := defaultNotificationLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxNotificationLimit {
		limit = maxNotificationLimit
	}
	rawCursor, exists := ctx.GetQuery("cursor")
	if !exists || strings.TrimSpace(rawCursor) == "" {
		return limit, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil {
		return 0, nil, errors.New("invalid cursor")
	}
	var cursor notificationCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != notificationCursorVersion || cursor.ID == 0 || cursor.ActivityAt.IsZero() {
		return 0, nil, errors.New("invalid cursor")
	}
	return limit, &cursor, nil
}

func encodeNotificationCursor(cursor notificationCursor) (string, error) {
	cursor.Version = notificationCursorVersion
	if cursor.ID == 0 || cursor.ActivityAt.IsZero() {
		return "", errors.New("invalid notification cursor")
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func GetMyNotifications(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	limit, cursor, err := parseNotificationQuery(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	query := visibleNotificationsForViewer(global.Db, viewerID, time.Now().UTC())
	if cursor != nil {
		query = query.Where("(n.activity_at < ?) OR (n.activity_at = ? AND n.id < ?)", cursor.ActivityAt, cursor.ActivityAt, cursor.ID)
	}
	var rows []notificationQueryRow
	if err := query.Order("n.activity_at DESC, n.id DESC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	response := notificationPageResponse{Items: make([]notificationResponse, 0, minInt(len(rows), limit))}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		response.Items = append(response.Items, notificationResponse{
			ID: row.ID, Type: row.Type,
			Actor:     notificationActorResponse{ID: row.ActorID, Username: row.Username, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL},
			ArticleID: row.ArticleID, CommentID: row.CommentID, ActivityAt: row.ActivityAt, Read: row.ReadAt != nil,
		})
	}
	if hasMore && len(rows) > 0 {
		next, err := encodeNotificationCursor(notificationCursor{ActivityAt: rows[len(rows)-1].ActivityAt, ID: rows[len(rows)-1].ID})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		response.NextCursor = &next
	}
	ctx.JSON(http.StatusOK, response)
}

func GetMyUnreadNotificationCount(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	var count int64
	query := visibleNotificationsForViewer(global.Db, viewerID, time.Now().UTC())
	if err := query.Where("n.read_at IS NULL").Count(&count).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"unread_count": count})
}

func MarkMyNotificationRead(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification id"})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	readAt := time.Now().UTC()
	result := global.Db.Model(&models.Notification{}).
		Where("id = ? AND recipient_id = ?", id, viewerID).
		Updates(map[string]interface{}{"read_at": readAt, "updated_at": readAt})
	if result.Error != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if result.RowsAffected == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
		return
	}
	ctx.Status(http.StatusNoContent)
}

func MarkMyNotificationsReadAll(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	readAt := time.Now().UTC()
	result := global.Db.Model(&models.Notification{}).
		Where("recipient_id = ? AND read_at IS NULL", viewerID).
		Updates(map[string]interface{}{"read_at": readAt, "updated_at": readAt})
	if result.Error != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
