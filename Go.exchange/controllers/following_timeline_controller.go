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
	defaultFollowingTimelineLimit = 20
	maxFollowingTimelineLimit     = 50
)

type followingTimelineResponse struct {
	Items      []articleResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type followingTimelineCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint      `json:"id"`
}

var loadActiveFollowingViewer = loadActiveFollowingViewerFromDB
var loadFollowingTimelinePage = loadFollowingTimelinePageFromDB

func loadActiveFollowingViewerFromDB(id uint) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	var user models.User
	return global.Db.Select("id").First(&user, id).Error
}

func GetFollowingTimeline(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if err := loadActiveFollowingViewer(viewerID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		} else {
			writeFollowingTimelineStoreError(ctx)
		}
		return
	}

	limit, err := parseFollowingTimelineLimit(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cursor, err := parseFollowingTimelineCursor(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
		return
	}

	response, err := loadFollowingTimelinePage(viewerID, limit, cursor)
	if err != nil {
		writeFollowingTimelineStoreError(ctx)
		return
	}
	if response.Items == nil {
		response.Items = make([]articleResponse, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func parseFollowingTimelineLimit(ctx *gin.Context) (int, error) {
	limit := defaultFollowingTimelineLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxFollowingTimelineLimit {
		limit = maxFollowingTimelineLimit
	}
	return limit, nil
}

func parseFollowingTimelineCursor(ctx *gin.Context) (*followingTimelineCursor, error) {
	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return nil, nil
	}
	cursor, err := decodeFollowingTimelineCursor(raw)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func encodeFollowingTimelineCursor(cursor followingTimelineCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeFollowingTimelineCursor(raw string) (followingTimelineCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return followingTimelineCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return followingTimelineCursor{}, errors.New("invalid cursor")
	}
	var cursor followingTimelineCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return followingTimelineCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func loadFollowingTimelinePageFromDB(viewerID uint, limit int, cursor *followingTimelineCursor) (followingTimelineResponse, error) {
	if global.Db == nil {
		return followingTimelineResponse{}, errors.New("database is not initialized")
	}

	query := global.Db.
		Model(&models.Article{}).
		Select(articleListSelectColumns).
		Joins("JOIN user_follows AS uf ON uf.following_id = articles.author_id").
		Joins("JOIN users AS followed_user ON followed_user.id = uf.following_id AND followed_user.deleted_at IS NULL").
		Where("uf.follower_id = ?", viewerID).
		Scopes(func(tx *gorm.DB) *gorm.DB { return visibleArticleScope(tx, time.Now()) })
	if cursor != nil {
		query = query.Where(
			"(articles.created_at < ?) OR (articles.created_at = ? AND articles.id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}

	articles, err := loadArticleResponses(query.Order("articles.created_at DESC, articles.id DESC").Limit(limit + 1))
	if err != nil {
		return followingTimelineResponse{}, err
	}
	return buildFollowingTimelineResponse(articles, limit)
}

func buildFollowingTimelineResponse(articles []articleResponse, limit int) (followingTimelineResponse, error) {
	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}
	items := make([]articleResponse, len(articles))
	copy(items, articles)
	response := followingTimelineResponse{Items: items}
	if hasMore {
		last := items[len(items)-1]
		nextCursor, err := encodeFollowingTimelineCursor(followingTimelineCursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
		if err != nil {
			return followingTimelineResponse{}, err
		}
		response.NextCursor = &nextCursor
	}
	return response, nil
}

func writeFollowingTimelineStoreError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
