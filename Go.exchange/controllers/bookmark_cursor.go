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
	bookmarkHistoryCursorVersion = 1
	defaultBookmarkHistoryLimit  = 20
	maxBookmarkHistoryLimit      = 50
)

type bookmarkHistoryCursor struct {
	Version      int       `json:"v"`
	BookmarkedAt time.Time `json:"bookmarked_at"`
	PostID       uint      `json:"post_id"`
}

func parseBookmarkHistoryPageQuery(ctx *gin.Context) (int, *bookmarkHistoryCursor, error) {
	limit := defaultBookmarkHistoryLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxBookmarkHistoryLimit {
		limit = maxBookmarkHistoryLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodeBookmarkHistoryCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodeBookmarkHistoryCursor(cursor bookmarkHistoryCursor) (string, error) {
	if cursor.Version != bookmarkHistoryCursorVersion || cursor.BookmarkedAt.IsZero() || cursor.PostID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeBookmarkHistoryCursor(raw string) (bookmarkHistoryCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return bookmarkHistoryCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return bookmarkHistoryCursor{}, errors.New("invalid cursor")
	}
	var cursor bookmarkHistoryCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return bookmarkHistoryCursor{}, errors.New("invalid cursor")
	}
	if cursor.Version != bookmarkHistoryCursorVersion || cursor.BookmarkedAt.IsZero() || cursor.PostID == 0 {
		return bookmarkHistoryCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeBookmarkHistoryQueryError(ctx *gin.Context, err error) {
	if err == nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}
