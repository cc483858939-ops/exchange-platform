package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultPostRelationLimit = 20
	maxPostRelationLimit     = 50
)

type postRelationCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint      `json:"id"`
}

func parsePostRelationLimit(ctx *gin.Context) (int, error) {
	return parsePageLimit(ctx, defaultPostRelationLimit, maxPostRelationLimit)
}

func parsePostRelationCursor(ctx *gin.Context) (*postRelationCursor, error) {
	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return nil, nil
	}
	cursor, err := decodePostRelationCursor(raw)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func encodePostRelationCursor(cursor postRelationCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodePostRelationCursor(raw string) (postRelationCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return postRelationCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return postRelationCursor{}, errors.New("invalid cursor")
	}
	var cursor postRelationCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return postRelationCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func parseReplyLimit(ctx *gin.Context) (int, error) {
	return parsePostRelationLimit(ctx)
}

func parseReplyCursor(ctx *gin.Context) (*replyCursor, error) {
	return parsePostRelationCursor(ctx)
}

func encodeReplyCursor(cursor replyCursor) (string, error) {
	return encodePostRelationCursor(cursor)
}

func decodeReplyCursor(raw string) (replyCursor, error) {
	return decodePostRelationCursor(raw)
}

const (
	defaultReplyLimit = defaultPostRelationLimit
	maxReplyLimit     = maxPostRelationLimit
)
