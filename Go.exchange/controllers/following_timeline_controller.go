package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type followingActivityType string

const (
	followingActivityPost   followingActivityType = "post"
	followingActivityRepost followingActivityType = "repost"

	defaultFollowingLimit = 20
	maxFollowingLimit     = 50
)

type followingTimelineItem struct {
	ActivityType followingActivityType `json:"activity_type"`
	ActivityAt   time.Time             `json:"activity_at"`
	SourceID     uint                  `json:"source_id"`
	Actor        publicAuthorResponse  `json:"actor"`
	Article      articleResponse       `json:"article"`
}

type followingTimelinePageResponse struct {
	Items      []followingTimelineItem `json:"items"`
	NextCursor *string                 `json:"next_cursor"`
}

type followingCursor struct {
	ActivityAt   time.Time `json:"activity_at"`
	ActivityType string    `json:"activity_type"`
	SourceID     uint      `json:"source_id"`
}

type followingActivityQueryRow struct {
	ActivityType string    `gorm:"column:activity_type"`
	ActivityAt   time.Time `gorm:"column:activity_at"`
	SourceID     uint      `gorm:"column:source_id"`
	ArticleID    uint      `gorm:"column:article_id"`
	ActorID      uint      `gorm:"column:actor_id"`
	ActivityRank int       `gorm:"column:activity_rank"`
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
			writeArticleTimelineStoreError(ctx)
		}
		return
	}

	limit, cursor, err := parseFollowingPageQuery(ctx)
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
		response.Items = make([]followingTimelineItem, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func parseFollowingPageQuery(ctx *gin.Context) (int, *followingCursor, error) {
	limit := defaultFollowingLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxFollowingLimit {
		limit = maxFollowingLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodeFollowingCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodeFollowingCursor(cursor followingCursor) (string, error) {
	if cursor.ActivityAt.IsZero() || cursor.SourceID == 0 || followingActivityRank(cursor.ActivityType) == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeFollowingCursor(raw string) (followingCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return followingCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return followingCursor{}, errors.New("invalid cursor")
	}
	var cursor followingCursor
	if err := json.Unmarshal(payload, &cursor); err != nil ||
		cursor.ActivityAt.IsZero() || cursor.SourceID == 0 || followingActivityRank(cursor.ActivityType) == 0 {
		return followingCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func followingActivityRank(activityType string) int {
	switch followingActivityType(activityType) {
	case followingActivityPost:
		return 1
	case followingActivityRepost:
		return 2
	default:
		return 0
	}
}

func loadFollowingTimelinePageFromDB(viewerID uint, limit int, cursor *followingCursor) (followingTimelinePageResponse, error) {
	if global.Db == nil {
		return followingTimelinePageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return followingTimelinePageResponse{}, errors.New("invalid limit")
	}

	now := time.Now().UTC()
	query := `
WITH activities AS (
    SELECT
        'post'::text AS activity_type,
        articles.published_at AS activity_at,
        articles.id AS source_id,
        articles.id AS article_id,
        articles.author_id AS actor_id,
        1::int AS activity_rank
    FROM articles
    JOIN user_follows AS direct_follow
      ON direct_follow.following_id = articles.author_id
     AND direct_follow.follower_id = ?
    JOIN users AS direct_author
      ON direct_author.id = articles.author_id
     AND direct_author.deleted_at IS NULL
    WHERE articles.deleted_at IS NULL
      AND articles.publication_state = ?
      AND articles.published_at IS NOT NULL
      AND articles.published_at <= ?
      AND (articles.expired_at IS NULL OR articles.expired_at > ?)

    UNION ALL

    SELECT
        'repost'::text AS activity_type,
        article_reposts.created_at AS activity_at,
        article_reposts.id AS source_id,
        articles.id AS article_id,
        article_reposts.user_id AS actor_id,
        2::int AS activity_rank
    FROM article_reposts
    JOIN user_follows AS repost_follow
      ON repost_follow.following_id = article_reposts.user_id
     AND repost_follow.follower_id = ?
    JOIN users AS reposter
      ON reposter.id = article_reposts.user_id
     AND reposter.deleted_at IS NULL
    JOIN articles
      ON articles.id = article_reposts.article_id
    JOIN users AS canonical_author
      ON canonical_author.id = articles.author_id
     AND canonical_author.deleted_at IS NULL
    WHERE articles.deleted_at IS NULL
      AND articles.publication_state = ?
      AND articles.published_at IS NOT NULL
      AND articles.published_at <= ?
      AND (articles.expired_at IS NULL OR articles.expired_at > ?)
), latest AS (
    SELECT DISTINCT ON (article_id)
        activity_type,
        activity_at,
        source_id,
        article_id,
        actor_id,
        activity_rank
    FROM activities
    ORDER BY article_id, activity_at DESC, activity_rank DESC, source_id DESC
)
SELECT activity_type, activity_at, source_id, article_id, actor_id, activity_rank
FROM latest
`
	args := []interface{}{
		viewerID,
		consts.ArticlePublicationStatePublished,
		now,
		now,
		viewerID,
		consts.ArticlePublicationStatePublished,
		now,
		now,
	}
	if cursor != nil {
		rank := followingActivityRank(cursor.ActivityType)
		if rank == 0 {
			return followingTimelinePageResponse{}, errors.New("invalid cursor")
		}
		query += `
WHERE activity_at < ?
   OR (activity_at = ? AND (activity_rank < ? OR (activity_rank = ? AND source_id < ?)))
`
		args = append(args, cursor.ActivityAt, cursor.ActivityAt, rank, rank, cursor.SourceID)
	}
	query += `
ORDER BY activity_at DESC, activity_rank DESC, source_id DESC
LIMIT ?
`
	args = append(args, limit+1)

	var rows []followingActivityQueryRow
	if err := global.Db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return followingTimelinePageResponse{}, err
	}

	articleIDs := make([]uint, 0, len(rows))
	actorIDs := make([]uint, 0, len(rows))
	seenArticleIDs := make(map[uint]struct{}, len(rows))
	seenActorIDs := make(map[uint]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seenArticleIDs[row.ArticleID]; !exists {
			seenArticleIDs[row.ArticleID] = struct{}{}
			articleIDs = append(articleIDs, row.ArticleID)
		}
		if _, exists := seenActorIDs[row.ActorID]; !exists {
			seenActorIDs[row.ActorID] = struct{}{}
			actorIDs = append(actorIDs, row.ActorID)
		}
	}

	articlesByID := make(map[uint]articleResponse, len(articleIDs))
	if len(articleIDs) > 0 {
		articleResponses, err := loadArticleResponses(publicArticleScope(
			global.Db.Model(&models.Article{}).
				Select(publicArticleSelectColumns).
				Where("articles.id IN ?", articleIDs),
			now,
		))
		if err != nil {
			return followingTimelinePageResponse{}, err
		}
		for _, article := range articleResponses {
			articlesByID[article.ID] = article
		}
	}
	actorsByID, err := loadPublicAuthorsByIDs(actorIDs)
	if err != nil {
		return followingTimelinePageResponse{}, err
	}
	return buildFollowingTimelinePageResponse(rows, articlesByID, actorsByID, limit)
}

func buildFollowingTimelinePageResponse(
	rows []followingActivityQueryRow,
	articlesByID map[uint]articleResponse,
	actorsByID map[uint]publicAuthorResponse,
	limit int,
) (followingTimelinePageResponse, error) {
	if limit <= 0 {
		return followingTimelinePageResponse{}, errors.New("invalid limit")
	}
	hasMore := len(rows) > limit
	visibleRows := rows
	if hasMore {
		visibleRows = rows[:limit]
	}
	items := make([]followingTimelineItem, 0, len(visibleRows))
	for _, row := range visibleRows {
		activityType := followingActivityType(row.ActivityType)
		if followingActivityRank(row.ActivityType) == 0 || row.ActivityAt.IsZero() || row.SourceID == 0 {
			return followingTimelinePageResponse{}, errors.New("invalid following activity")
		}
		article, ok := articlesByID[row.ArticleID]
		if !ok {
			return followingTimelinePageResponse{}, errors.New("following article could not be found")
		}
		actor, ok := actorsByID[row.ActorID]
		if !ok {
			return followingTimelinePageResponse{}, errors.New("following activity actor could not be found")
		}
		items = append(items, followingTimelineItem{
			ActivityType: activityType,
			ActivityAt:   row.ActivityAt,
			SourceID:     row.SourceID,
			Actor:        actor,
			Article:      article,
		})
	}

	response := followingTimelinePageResponse{Items: items}
	if !hasMore {
		return response, nil
	}
	last := visibleRows[len(visibleRows)-1]
	nextCursor, err := encodeFollowingCursor(followingCursor{
		ActivityAt:   last.ActivityAt,
		ActivityType: last.ActivityType,
		SourceID:     last.SourceID,
	})
	if err != nil {
		return followingTimelinePageResponse{}, err
	}
	response.NextCursor = &nextCursor
	return response, nil
}
