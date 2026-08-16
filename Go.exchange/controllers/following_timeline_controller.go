package controllers

import (
	"errors"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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
			writeArticleTimelineStoreError(ctx)
		}
		return
	}

	limit, cursor, err := parseArticlePageQuery(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := loadFollowingTimelinePage(viewerID, limit, cursor)
	if err != nil {
		writeArticleTimelineStoreError(ctx)
		return
	}
	if response.Items == nil {
		response.Items = make([]articleResponse, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func loadFollowingTimelinePageFromDB(viewerID uint, limit int, cursor *articleCursor) (articlePageResponse, error) {
	if global.Db == nil {
		return articlePageResponse{}, errors.New("database is not initialized")
	}

	now := time.Now().UTC()
	query := global.Db.
		Model(&models.Article{}).
		Select(publicArticleSelectColumns).
		Joins("JOIN user_follows AS uf ON uf.following_id = articles.author_id").
		Joins("JOIN users AS followed_user ON followed_user.id = uf.following_id AND followed_user.deleted_at IS NULL").
		Where("uf.follower_id = ?", viewerID).
		Scopes(func(tx *gorm.DB) *gorm.DB { return publicArticleScope(tx, now) })
	if cursor != nil {
		query = query.Where(
			"(articles.published_at < ?) OR (articles.published_at = ? AND articles.id < ?)",
			cursor.PublishedAt,
			cursor.PublishedAt,
			cursor.ID,
		)
	}

	articles, err := loadArticleResponses(query.Order("articles.published_at DESC, articles.id DESC").Limit(limit + 1))
	if err != nil {
		return articlePageResponse{}, err
	}
	return buildArticlePageResponse(articles, limit)
}
