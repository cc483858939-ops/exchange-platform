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
	Post         postResponse          `json:"post"`
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
	PostID       uint      `gorm:"column:post_id"`
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
			writePostTimelineStoreError(ctx)
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
		writePostTimelineStoreError(ctx)
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
	    posts.created_at AS activity_at,
        posts.id AS source_id,
        posts.id AS post_id,
        posts.author_id AS actor_id,
        1::int AS activity_rank
	FROM posts
	JOIN user_follows AS direct_follow
      ON direct_follow.following_id = posts.author_id
     AND direct_follow.follower_id = ?
    WHERE posts.reply_to_post_id IS NULL
	      AND ` + publicPostEligibilitySQL("posts") + `

    UNION ALL

    SELECT
        'repost'::text AS activity_type,
        post_reposts.created_at AS activity_at,
        post_reposts.id AS source_id,
        posts.id AS post_id,
        post_reposts.user_id AS actor_id,
        2::int AS activity_rank
    FROM post_reposts
    JOIN user_follows AS repost_follow
      ON repost_follow.following_id = post_reposts.user_id
     AND repost_follow.follower_id = ?
    JOIN users AS reposter
      ON reposter.id = post_reposts.user_id
     AND reposter.deleted_at IS NULL
	JOIN posts
	  ON posts.id = post_reposts.post_id
    JOIN users AS canonical_author
      ON canonical_author.id = posts.author_id
     AND canonical_author.deleted_at IS NULL
	WHERE ` + publicPostEligibilitySQL("posts") + `
), latest AS (
    SELECT DISTINCT ON (post_id)
        activity_type,
        activity_at,
        source_id,
        post_id,
        actor_id,
        activity_rank
    FROM activities
    ORDER BY post_id, activity_at DESC, activity_rank DESC, source_id DESC
)
SELECT activity_type, activity_at, source_id, post_id, actor_id, activity_rank
FROM latest
`
	args := []interface{}{
		viewerID,
		viewerID,
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

	postIDs := make([]uint, 0, len(rows))
	actorIDs := make([]uint, 0, len(rows))
	seenPostIDs := make(map[uint]struct{}, len(rows))
	seenActorIDs := make(map[uint]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seenPostIDs[row.PostID]; !exists {
			seenPostIDs[row.PostID] = struct{}{}
			postIDs = append(postIDs, row.PostID)
		}
		if _, exists := seenActorIDs[row.ActorID]; !exists {
			seenActorIDs[row.ActorID] = struct{}{}
			actorIDs = append(actorIDs, row.ActorID)
		}
	}

	postsByID := make(map[uint]postResponse, len(postIDs))
	if len(postIDs) > 0 {
		postResponses, err := loadPostResponses(publicPostScope(
			global.Db.Model(&models.Post{}).
				Select(publicPostSelectColumns).
				Where("posts.id IN ?", postIDs),
			now,
		))
		if err != nil {
			return followingTimelinePageResponse{}, err
		}
		for _, post := range postResponses {
			postsByID[post.ID] = post
		}
	}
	actorsByID, err := loadPublicAuthorsByIDs(actorIDs)
	if err != nil {
		return followingTimelinePageResponse{}, err
	}
	return buildFollowingTimelinePageResponse(rows, postsByID, actorsByID, limit)
}

func buildFollowingTimelinePageResponse(
	rows []followingActivityQueryRow,
	postsByID map[uint]postResponse,
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
		post, ok := postsByID[row.PostID]
		if !ok {
			return followingTimelinePageResponse{}, errors.New("following post could not be found")
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
			Post:         post,
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
