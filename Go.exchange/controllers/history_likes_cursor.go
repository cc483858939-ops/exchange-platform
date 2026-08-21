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
	likedHistoryCursorVersion = 1
	defaultLikedHistoryLimit  = 20
	maxLikedHistoryLimit      = 50
)

type likedHistoryCursor struct {
	Version        int       `json:"v"`
	StateChangedAt time.Time `json:"state_changed_at"`
	ArticleID      uint      `json:"article_id"`
}

func parseLikedHistoryPageQuery(ctx *gin.Context) (int, *likedHistoryCursor, error) {
	limit := defaultLikedHistoryLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxLikedHistoryLimit {
		limit = maxLikedHistoryLimit
	}

	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return limit, nil, nil
	}
	cursor, err := decodeLikedHistoryCursor(raw)
	if err != nil {
		return 0, nil, err
	}
	return limit, &cursor, nil
}

func encodeLikedHistoryCursor(cursor likedHistoryCursor) (string, error) {
	if cursor.Version != likedHistoryCursorVersion || cursor.StateChangedAt.IsZero() || cursor.ArticleID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeLikedHistoryCursor(raw string) (likedHistoryCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return likedHistoryCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return likedHistoryCursor{}, errors.New("invalid cursor")
	}
	var cursor likedHistoryCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return likedHistoryCursor{}, errors.New("invalid cursor")
	}
	if cursor.Version != likedHistoryCursorVersion || cursor.StateChangedAt.IsZero() || cursor.ArticleID == 0 {
		return likedHistoryCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeLikedHistoryQueryError(ctx *gin.Context, err error) {
	if err == nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}
