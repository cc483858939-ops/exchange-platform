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
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	var user models.User
	return global.APIDb.WithContext(ctx).Select("id").First(&user, id).Error
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
	if global.APIDb == nil {
		return timelinePageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return timelinePageResponse{}, errors.New("invalid limit")
	}
	db := global.APIDb.WithContext(ctx)

	now := time.Now().UTC()
	var query string
	var args []interface{}
	if cursor == nil {
		query = followingTimelineFirstPageSQL()
		args = []interface{}{viewerID, limit + 1, limit + 1, limit + 1}
	} else {
		rank := timelineActivityRank(cursor.ActivityType)
		if rank == 0 {
			return timelinePageResponse{}, errors.New("invalid cursor")
		}
		query = followingTimelineCursorSQL()
		args = []interface{}{
			viewerID, viewerID,
			cursor.ActivityAt, cursor.ActivityAt, rank, rank, cursor.SourceID,
			limit + 1,
		}
	}

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
		postResponses, err := loadTimelinePostResponses(publicPostScope(
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
