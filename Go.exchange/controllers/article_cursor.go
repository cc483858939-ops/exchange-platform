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
	articleCursorVersion = 1
	defaultArticleLimit  = 20
	maxArticleLimit      = 50
)

type articleCursor struct {
	Version     int       `json:"v"`
	PublishedAt time.Time `json:"published_at"`
	ID          uint      `json:"id"`
}

type articlePageResponse struct {
	Items      []articleResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func parseArticlePageQuery(ctx *gin.Context) (int, *articleCursor, error) {
	limit := defaultArticleLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxArticleLimit {
		limit = maxArticleLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodeArticleCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodeArticleCursor(cursor articleCursor) (string, error) {
	if cursor.Version != articleCursorVersion || cursor.PublishedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeArticleCursor(raw string) (articleCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return articleCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return articleCursor{}, errors.New("invalid cursor")
	}
	var cursor articleCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != articleCursorVersion || cursor.PublishedAt.IsZero() || cursor.ID == 0 {
		return articleCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func buildArticlePageResponse(articles []articleResponse, limit int) (articlePageResponse, error) {
	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}
	items := make([]articleResponse, len(articles))
	copy(items, articles)
	response := articlePageResponse{Items: items}
	if !hasMore {
		return response, nil
	}

	last := items[len(items)-1]
	if last.PublishedAt == nil {
		return articlePageResponse{}, errors.New("public article is missing published_at")
	}
	nextCursor, err := encodeArticleCursor(articleCursor{
		Version:     articleCursorVersion,
		PublishedAt: *last.PublishedAt,
		ID:          last.ID,
	})
	if err != nil {
		return articlePageResponse{}, err
	}
	response.NextCursor = &nextCursor
	return response, nil
}

func writeArticleTimelineStoreError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
