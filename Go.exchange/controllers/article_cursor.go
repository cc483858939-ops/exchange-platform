package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	postCursorVersion = 2
	defaultPostLimit  = 20
	maxPostLimit      = 50
)

type postCursor struct {
	Version     int       `json:"v"`
	PublishedAt time.Time `json:"published_at"`
	ID          uint      `json:"id"`
}

type postPageResponse struct {
	Items      []postResponse `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func parsePostPageQuery(ctx *gin.Context) (int, *postCursor, error) {
	limit := defaultPostLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxPostLimit {
		limit = maxPostLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodePostCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodePostCursor(cursor postCursor) (string, error) {
	if cursor.Version != postCursorVersion || cursor.PublishedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodePostCursor(raw string) (postCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return postCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return postCursor{}, errors.New("invalid cursor")
	}
	var cursor postCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != postCursorVersion || cursor.PublishedAt.IsZero() || cursor.ID == 0 {
		return postCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func buildPostPageResponse(posts []postResponse, limit int) (postPageResponse, error) {
	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	items := make([]postResponse, len(posts))
	copy(items, posts)
	response := postPageResponse{Items: items}
	if !hasMore {
		return response, nil
	}

	last := items[len(items)-1]
	if last.PublishedAt == nil {
		return postPageResponse{}, errors.New("public post is missing published_at")
	}
	nextCursor, err := encodePostCursor(postCursor{
		Version:     postCursorVersion,
		PublishedAt: *last.PublishedAt,
		ID:          last.ID,
	})
	if err != nil {
		return postPageResponse{}, err
	}
	response.NextCursor = &nextCursor
	return response, nil
}

func writePostTimelineStoreError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
