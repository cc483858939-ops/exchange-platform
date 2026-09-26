package controllers

import (
	"context"
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

func loadActiveFollowingViewerFromDB(ctx context.Context, id uint) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	var user models.User
	return global.Db.WithContext(ctx).Select("id").First(&user, id).Error
}

func GetFollowingTimeline(ctx *gin.Context) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if err := loadActiveFollowingViewer(ctx.Request.Context(), viewerID); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		} else {
			writePostTimelineStoreError(ctx, err)
		}
		return
	}

	limit, cursor, err := parseTimelinePageQuery(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := loadFollowingTimelinePage(ctx.Request.Context(), viewerID, limit, cursor)
	if err != nil {
		writePostTimelineStoreError(ctx, err)
		return
	}
	if response.Items == nil {
		response.Items = make([]timelineItem, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func loadFollowingTimelinePageFromDB(ctx context.Context, viewerID uint, limit int, cursor *timelineCursor) (timelinePageResponse, error) {
	if global.Db == nil {
		return timelinePageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return timelinePageResponse{}, errors.New("invalid limit")
	}
	db := global.Db.WithContext(ctx)

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
		rank := timelineActivityRank(cursor.ActivityType)
		if rank == 0 {
			return timelinePageResponse{}, errors.New("invalid cursor")
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

	var rows []timelineActivityQueryRow
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return timelinePageResponse{}, err
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
			db.Model(&models.Post{}).
				Select(publicPostSelectColumns).
				Where("posts.id IN ?", postIDs),
			now,
		))
		if err != nil {
			return timelinePageResponse{}, err
		}
		for _, post := range postResponses {
			postsByID[post.ID] = post
		}
	}
	actorsByID, err := loadPublicAuthorsByIDs(ctx, actorIDs)
	if err != nil {
		return timelinePageResponse{}, err
	}
	return buildTimelinePageResponse(rows, postsByID, actorsByID, limit)
}
