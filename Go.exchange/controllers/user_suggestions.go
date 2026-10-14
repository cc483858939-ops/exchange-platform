package controllers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/recommendation"

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
	UserID          uint      `gorm:"column:user_id"`
	Username        string    `gorm:"column:username"`
	DisplayName     string    `gorm:"column:display_name"`
	Bio             string    `gorm:"column:bio"`
	AvatarURL       string    `gorm:"column:avatar_url"`
	UserCreatedAt   time.Time `gorm:"column:user_created_at"`
	RecentPostCount int64     `gorm:"column:recent_post_count"`
	LatestPostAt    time.Time `gorm:"column:latest_post_at"`
	RawAffinity     float64   `gorm:"column:raw_affinity"`
}

const suggestedUserAffinityWeight = 0.75

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
	  AND NOT EXISTS (
		SELECT 1 FROM user_mutes AS um
		WHERE um.muter_id = ? AND um.muted_id = p.author_id
	  )
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
	   candidate.created_at AS user_created_at,
	   author_activity.recent_post_count AS recent_post_count,
	   author_activity.latest_post_at AS latest_post_at,
	   COALESCE(author_affinity.raw_affinity, 0) AS raw_affinity
FROM author_activity
JOIN users AS candidate
	ON candidate.id = author_activity.author_id
	AND candidate.deleted_at IS NULL
LEFT JOIN user_author_affinities AS author_affinity
	ON author_affinity.user_id = ?
	AND author_affinity.author_id = candidate.id
LEFT JOIN user_follows AS viewer_follow
	ON viewer_follow.follower_id = ?
	AND viewer_follow.following_id = candidate.id
WHERE candidate.id <> ?
	AND viewer_follow.id IS NULL`

	var rows []suggestedUserQueryRow
	if err := global.APIDb.WithContext(ctx).Raw(
		query,
		viewerID,
		cutoff,
		now,
		suggestedUserPostCandidateLimit,
		suggestedUserActivityCountCap,
		viewerID,
		viewerID,
		viewerID,
	).Scan(&rows).Error; err != nil {
		return userConnectionPageResponse{}, err
	}
	rows = rankSuggestedUserRows(rows, now, suggestedUserAffinitySaturationScale(), limit)

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

func suggestedUserAffinitySaturationScale() float64 {
	if config.AppConfig != nil {
		scale := config.AppConfig.Recommendation.AuthorAffinitySaturationScale
		if scale > 0 && !math.IsNaN(scale) && !math.IsInf(scale, 0) {
			return scale
		}
	}
	return recommendation.DefaultConfig().AuthorAffinitySaturationScale
}

func rankSuggestedUserRows(rows []suggestedUserQueryRow, now time.Time, saturationScale float64, limit int) []suggestedUserQueryRow {
	if !validPositiveFinite(saturationScale) {
		saturationScale = recommendation.DefaultConfig().AuthorAffinitySaturationScale
	}

	personalized := false
	for _, row := range rows {
		if validPositiveFinite(row.RawAffinity) {
			personalized = true
			break
		}
	}

	if !personalized {
		sort.Slice(rows, func(i, j int) bool {
			return suggestedUserActivityBefore(rows[i], rows[j])
		})
	} else {
		sort.Slice(rows, func(i, j int) bool {
			scoreI := suggestedUserFinalScore(rows[i], now, saturationScale)
			scoreJ := suggestedUserFinalScore(rows[j], now, saturationScale)
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			return suggestedUserActivityBefore(rows[i], rows[j])
		})
	}

	if limit >= 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func suggestedUserActivityBefore(left, right suggestedUserQueryRow) bool {
	if left.RecentPostCount != right.RecentPostCount {
		return left.RecentPostCount > right.RecentPostCount
	}
	if !left.LatestPostAt.Equal(right.LatestPostAt) {
		return left.LatestPostAt.After(right.LatestPostAt)
	}
	return left.UserID < right.UserID
}

func suggestedUserFinalScore(row suggestedUserQueryRow, now time.Time, saturationScale float64) float64 {
	postCount := row.RecentPostCount
	if postCount < 0 {
		postCount = 0
	} else if postCount > suggestedUserActivityCountCap {
		postCount = suggestedUserActivityCountCap
	}

	recencyScore := 0.0
	if !row.LatestPostAt.IsZero() {
		recencyScore = 1 - now.Sub(row.LatestPostAt).Hours()/(suggestedUserLookback.Hours())
		if recencyScore < 0 {
			recencyScore = 0
		} else if recencyScore > 1 {
			recencyScore = 1
		}
	}

	activityScore := 0.70*float64(postCount)/float64(suggestedUserActivityCountCap) + 0.30*recencyScore
	affinityScore := 0.0
	if validPositiveFinite(row.RawAffinity) {
		affinityScore = math.Tanh(row.RawAffinity / saturationScale)
	}
	return activityScore + suggestedUserAffinityWeight*affinityScore
}

func validPositiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
