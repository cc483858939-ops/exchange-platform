package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"Go.exchange/global"

	"github.com/gin-gonic/gin"
)

const (
	defaultSuggestedUserLimit       = 3
	maxSuggestedUserLimit           = 5
	suggestedUserPostCandidateLimit = 500
	suggestedUserActivityCountCap   = 5
	suggestedUserLookback           = 30 * 24 * time.Hour
)

type suggestedUserQueryRow struct {
	UserID        uint      `gorm:"column:user_id"`
	Username      string    `gorm:"column:username"`
	DisplayName   string    `gorm:"column:display_name"`
	Bio           string    `gorm:"column:bio"`
	AvatarURL     string    `gorm:"column:avatar_url"`
	UserCreatedAt time.Time `gorm:"column:user_created_at"`
}

var loadSuggestedUsers = loadSuggestedUsersFromDB

func GetSuggestedUsers(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	limit, err := parseSuggestedUserLimit(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	page, err := loadSuggestedUsers(ctx.Request.Context(), viewerID, limit)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load user suggestions"})
		return
	}
	if page.Items == nil {
		page.Items = make([]userConnectionResponse, 0)
	}
	ctx.JSON(http.StatusOK, page)
}

func parseSuggestedUserLimit(ctx *gin.Context) (int, error) {
	if ctx.Request.URL.Query().Has("offset") {
		return 0, errors.New("offset is not supported")
	}
	values, exists := ctx.Request.URL.Query()["limit"]
	if !exists {
		return defaultSuggestedUserLimit, nil
	}
	if len(values) != 1 {
		return 0, errors.New("invalid limit")
	}

	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > maxSuggestedUserLimit {
		return 0, errors.New("invalid limit")
	}
	return limit, nil
}

func loadSuggestedUsersFromDB(ctx context.Context, viewerID uint, limit int) (userConnectionPageResponse, error) {
	if global.APIDb == nil {
		return userConnectionPageResponse{}, errors.New("database is not initialized")
	}

	now := time.Now().UTC()
	cutoff := now.Add(-suggestedUserLookback)
	const query = `
WITH post_candidates AS MATERIALIZED (
	SELECT p.author_id, p.created_at, p.id
	FROM posts AS p
	WHERE p.deleted_at IS NULL
	  AND p.visibility = 'public'
	  AND p.reply_to_post_id IS NULL
	  AND p.created_at >= ?
	  AND p.created_at <= ?
	ORDER BY p.created_at DESC, p.id DESC
	LIMIT ?
), author_activity AS (
	SELECT author_id,
	       LEAST(COUNT(*), ?) AS recent_post_count,
	       MAX(created_at) AS latest_post_at
	FROM post_candidates
	GROUP BY author_id
)
SELECT candidate.id AS user_id,
	   candidate.username AS username,
	   candidate.display_name AS display_name,
	   candidate.bio AS bio,
	   candidate.avatar_url AS avatar_url,
	   candidate.created_at AS user_created_at
FROM author_activity
JOIN users AS candidate
	ON candidate.id = author_activity.author_id
	AND candidate.deleted_at IS NULL
LEFT JOIN user_follows AS viewer_follow
	ON viewer_follow.follower_id = ?
	AND viewer_follow.following_id = candidate.id
WHERE candidate.id <> ?
	AND viewer_follow.id IS NULL
ORDER BY author_activity.recent_post_count DESC,
	 author_activity.latest_post_at DESC,
	 candidate.id ASC
LIMIT ?`

	var rows []suggestedUserQueryRow
	if err := global.APIDb.WithContext(ctx).Raw(
		query,
		cutoff,
		now,
		suggestedUserPostCandidateLimit,
		suggestedUserActivityCountCap,
		viewerID,
		viewerID,
		limit,
	).Scan(&rows).Error; err != nil {
		return userConnectionPageResponse{}, err
	}

	page := userConnectionPageResponse{Items: make([]userConnectionResponse, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, userConnectionResponse{
			User: publicUserSummaryResponse{
				ID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
				Bio: row.Bio, AvatarURL: row.AvatarURL, CreatedAt: row.UserCreatedAt,
			},
			Following: false,
		})
	}
	return page, nil
}
