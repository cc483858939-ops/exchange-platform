package controllers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

const (
	defaultTopicPostLimit = 20
	maxTopicPostLimit     = 50
)

type topicSummaryResponse struct {
	Slug        string `json:"slug"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type topicListResponse struct {
	Items []topicSummaryResponse `json:"items"`
}

type topicPostsResponse struct {
	Topic      topicSummaryResponse `json:"topic"`
	Items      []postResponse       `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

type topicPostCursor struct {
	CreatedAt time.Time `json:"created_at"`
	PostID    uint      `json:"post_id"`
}

type topicPostQueryRow struct {
	ID        uint      `gorm:"column:id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

var loadTopicConfiguration = config.LoadCuratedTopics
var loadTopicPostsPage = loadTopicPostsPageFromDB

func GetTopics(ctx *gin.Context) {
	catalog, err := loadTopicConfiguration()
	if err != nil {
		writeTopicInternalError(ctx)
		return
	}
	items := make([]topicSummaryResponse, 0, len(catalog.Topics))
	for _, topic := range catalog.Topics {
		if topic.Enabled {
			items = append(items, topicSummaryFromConfig(topic))
		}
	}
	ctx.JSON(http.StatusOK, topicListResponse{Items: items})
}

func GetTopicPosts(ctx *gin.Context) {
	catalog, err := loadTopicConfiguration()
	if err != nil {
		writeTopicInternalError(ctx)
		return
	}
	topic, found := enabledTopicBySlug(catalog, ctx.Param("slug"))
	if !found {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "topic not found"})
		return
	}
	limit, cursor, err := parseTopicPostPageQuery(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.Db == nil {
		writeTopicInternalError(ctx)
		return
	}
	page, err := loadTopicPostsPage(topic, limit, cursor)
	if err != nil {
		writeTopicInternalError(ctx)
		return
	}
	if page.Items == nil {
		page.Items = make([]postResponse, 0)
	}
	ctx.JSON(http.StatusOK, topicPostsResponse{
		Topic: topicSummaryFromConfig(topic), Items: page.Items, NextCursor: page.NextCursor,
	})
}

func topicSummaryFromConfig(topic config.CuratedTopic) topicSummaryResponse {
	return topicSummaryResponse{Slug: topic.Slug, Label: topic.Label, Description: topic.Description}
}

func enabledTopicBySlug(catalog config.CuratedTopicsConfig, slug string) (config.CuratedTopic, bool) {
	for _, topic := range catalog.Topics {
		if topic.Enabled && topic.Slug == slug {
			return topic, true
		}
	}
	return config.CuratedTopic{}, false
}

func parseTopicPostPageQuery(ctx *gin.Context) (int, *topicPostCursor, error) {
	limit := defaultTopicPostLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, nil, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxTopicPostLimit {
		limit = maxTopicPostLimit
	}
	if raw, exists := ctx.GetQuery("cursor"); exists {
		cursor, err := decodeTopicPostCursor(raw)
		if err != nil {
			return 0, nil, err
		}
		return limit, &cursor, nil
	}
	return limit, nil, nil
}

func encodeTopicPostCursor(cursor topicPostCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || cursor.PostID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeTopicPostCursor(raw string) (topicPostCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return topicPostCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return topicPostCursor{}, errors.New("invalid cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor topicPostCursor
	if err := decoder.Decode(&cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.PostID == 0 {
		return topicPostCursor{}, errors.New("invalid cursor")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return topicPostCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func loadTopicPostsPageFromDB(topic config.CuratedTopic, limit int, cursor *topicPostCursor) (postPageResponse, error) {
	db := global.Db
	if db == nil {
		return postPageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return postPageResponse{}, errors.New("invalid limit")
	}
	if len(topic.SourceKeys) == 0 {
		return postPageResponse{Items: make([]postResponse, 0)}, nil
	}

	query := `
SELECT posts.id, posts.created_at
FROM devdata_mirror_posts AS mirror_posts
JOIN devdata_mirror_accounts AS mirror_accounts
  ON mirror_accounts.id = mirror_posts.mirror_account_id
JOIN posts
  ON posts.id = mirror_posts.local_post_id
WHERE mirror_accounts.registry_key IN ?
  AND mirror_accounts.enabled = TRUE
  AND mirror_posts.state = ?
  AND ` + publicPostEligibilitySQL("posts") + `
`
	args := []interface{}{topic.SourceKeys, models.DevDataMirrorPostStateActive}
	if cursor != nil {
		if cursor.CreatedAt.IsZero() || cursor.PostID == 0 {
			return postPageResponse{}, errors.New("invalid cursor")
		}
		query += `
  AND (posts.created_at < ? OR (posts.created_at = ? AND posts.id < ?))
`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.PostID)
	}
	query += `ORDER BY posts.created_at DESC, posts.id DESC LIMIT ?`
	args = append(args, limit+1)

	var rows []topicPostQueryRow
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return postPageResponse{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	page := postPageResponse{Items: make([]postResponse, 0, len(rows))}
	if len(rows) == 0 {
		return page, nil
	}

	postIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		postIDs = append(postIDs, row.ID)
	}
	responses, err := loadPostResponses(publicPostScope(
		db.Model(&models.Post{}).
			Select(publicPostSelectColumns).
			Where("posts.id IN ?", postIDs),
		time.Now().UTC(),
	))
	if err != nil {
		return postPageResponse{}, err
	}
	responsesByID := make(map[uint]postResponse, len(responses))
	for _, response := range responses {
		responsesByID[response.ID] = response
	}
	for _, row := range rows {
		if response, exists := responsesByID[row.ID]; exists {
			page.Items = append(page.Items, response)
		}
	}
	if hasMore && len(rows) > 0 {
		lastRow := rows[len(rows)-1]
		encoded, err := encodeTopicPostCursor(topicPostCursor{
			CreatedAt: lastRow.CreatedAt,
			PostID:    lastRow.ID,
		})
		if err != nil {
			return postPageResponse{}, fmt.Errorf("encode topic cursor: %w", err)
		}
		page.NextCursor = &encoded
	}
	return page, nil
}

func writeTopicInternalError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
