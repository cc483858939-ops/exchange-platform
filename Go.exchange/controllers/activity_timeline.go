package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type timelineActivityType string

const (
	timelineActivityPost   timelineActivityType = "post"
	timelineActivityRepost timelineActivityType = "repost"

	defaultTimelineLimit = 20
	maxTimelineLimit     = 50
)

type timelineItem struct {
	ActivityType timelineActivityType `json:"activity_type"`
	ActivityAt   time.Time            `json:"activity_at"`
	SourceID     uint                 `json:"source_id"`
	Actor        publicAuthorResponse `json:"actor"`
	Post         postResponse         `json:"post"`
}

type timelinePageResponse struct {
	Items      []timelineItem `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type timelineCursor struct {
	ActivityAt   time.Time `json:"activity_at"`
	ActivityType string    `json:"activity_type"`
	SourceID     uint      `json:"source_id"`
}

type timelineActivityQueryRow struct {
	ActivityType string    `gorm:"column:activity_type"`
	ActivityAt   time.Time `gorm:"column:activity_at"`
	SourceID     uint      `gorm:"column:source_id"`
	PostID       uint      `gorm:"column:post_id"`
	ActorID      uint      `gorm:"column:actor_id"`
	ActivityRank int       `gorm:"column:activity_rank"`
}

func parseTimelinePageQuery(ctx *gin.Context) (int, *timelineCursor, error) {
	limit := defaultTimelineLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxTimelineLimit {
		limit = maxTimelineLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodeTimelineCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodeTimelineCursor(cursor timelineCursor) (string, error) {
	if cursor.ActivityAt.IsZero() || cursor.SourceID == 0 || timelineActivityRank(cursor.ActivityType) == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeTimelineCursor(raw string) (timelineCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return timelineCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return timelineCursor{}, errors.New("invalid cursor")
	}
	var cursor timelineCursor
	if err := json.Unmarshal(payload, &cursor); err != nil ||
		cursor.ActivityAt.IsZero() || cursor.SourceID == 0 || timelineActivityRank(cursor.ActivityType) == 0 {
		return timelineCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func timelineActivityRank(activityType string) int {
	switch timelineActivityType(activityType) {
	case timelineActivityPost:
		return 1
	case timelineActivityRepost:
		return 2
	default:
		return 0
	}
}

func buildTimelinePageResponse(
	rows []timelineActivityQueryRow,
	postsByID map[uint]postResponse,
	actorsByID map[uint]publicAuthorResponse,
	limit int,
) (timelinePageResponse, error) {
	if limit <= 0 {
		return timelinePageResponse{}, errors.New("invalid limit")
	}
	hasMore := len(rows) > limit
	visibleRows := rows
	if hasMore {
		visibleRows = rows[:limit]
	}
	items := make([]timelineItem, 0, len(visibleRows))
	for _, row := range visibleRows {
		activityType := timelineActivityType(row.ActivityType)
		if timelineActivityRank(row.ActivityType) == 0 || row.ActivityAt.IsZero() || row.SourceID == 0 {
			return timelinePageResponse{}, errors.New("invalid timeline activity")
		}
		post, ok := postsByID[row.PostID]
		if !ok {
			return timelinePageResponse{}, errors.New("timeline post could not be found")
		}
		actor, ok := actorsByID[row.ActorID]
		if !ok {
			return timelinePageResponse{}, errors.New("timeline activity actor could not be found")
		}
		items = append(items, timelineItem{
			ActivityType: activityType,
			ActivityAt:   row.ActivityAt,
			SourceID:     row.SourceID,
			Actor:        actor,
			Post:         post,
		})
	}

	response := timelinePageResponse{Items: items}
	if !hasMore {
		return response, nil
	}
	last := visibleRows[len(visibleRows)-1]
	nextCursor, err := encodeTimelineCursor(timelineCursor{
		ActivityAt:   last.ActivityAt,
		ActivityType: last.ActivityType,
		SourceID:     last.SourceID,
	})
	if err != nil {
		return timelinePageResponse{}, err
	}
	response.NextCursor = &nextCursor
	return response, nil
}
