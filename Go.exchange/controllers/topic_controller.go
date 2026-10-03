package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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

type topicPostQueryRow struct {
	ID         uint  `gorm:"column:id"`
	ShuffleKey int64 `gorm:"column:shuffle_key"`
	Bucket     int64 `gorm:"-"`
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
	limit, cursor, err := parseTopicPostPageQuery(ctx, topic)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.APIDb == nil {
		writeTopicInternalError(ctx)
		return
	}
	page, err := loadTopicPostsPage(ctx.Request.Context(), topic, limit, cursor)
	if err != nil {
		if !handleRequestDBError(ctx, err) {
			writeTopicInternalError(ctx)
		}
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

func parseTopicPostPageQuery(ctx *gin.Context, topic config.CuratedTopic) (int, *topicPostCursorV2, error) {
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
		topicHash, err := topicCursorCriteriaHash(topic)
		if err != nil {
			return 0, nil, err
		}
		cursor, err := decodeTopicPostCursor(raw, topicHash)
		if err != nil {
			return 0, nil, errInvalidTopicCursor
		}
		return limit, &cursor, nil
	}
	return limit, nil, nil
}

func loadTopicPostsPageFromDB(ctx context.Context, topic config.CuratedTopic, limit int, cursor *topicPostCursorV2) (postPageResponse, error) {
	db := global.APIDb
	if db == nil {
		return postPageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return postPageResponse{}, errors.New("invalid limit")
	}
	traversal, err := resolveTopicTraversal(topic, cursor)
	if err != nil {
		return postPageResponse{}, err
	}
	return loadTopicPostsPageWithTraversal(ctx, db, topic, limit, traversal)
}

func loadTopicPostsPageWithTraversal(ctx context.Context, db *gorm.DB, topic config.CuratedTopic, limit int, traversal topicTraversal) (postPageResponse, error) {
	return loadTopicPostsPageWithTraversalAndHydrator(ctx, db, topic, limit, traversal, loadPostResponses)
}

func loadTopicPostsPageWithTraversalAndHydrator(ctx context.Context, db *gorm.DB, topic config.CuratedTopic, limit int, traversal topicTraversal, hydrate func(*gorm.DB) ([]postResponse, error)) (postPageResponse, error) {
	if db == nil {
		return postPageResponse{}, errors.New("database is not initialized")
	}
	if limit <= 0 {
		return postPageResponse{}, errors.New("invalid limit")
	}
	db = db.WithContext(ctx)
	page := postPageResponse{Items: make([]postResponse, 0)}
	if len(topic.SourceKeys) == 0 {
		return page, nil
	}

	rows, err := loadTopicTraversalCandidates(db, topic, limit+1, traversal)
	if err != nil {
		return postPageResponse{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return page, nil
	}

	postIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		postIDs = append(postIDs, row.ID)
	}
	responses, err := hydrate(publicPostScope(
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
	returnedRows := make([]topicPostQueryRow, 0, len(rows))
	for _, row := range rows {
		if response, exists := responsesByID[row.ID]; exists {
			page.Items = append(page.Items, response)
			returnedRows = append(returnedRows, row)
		}
	}
	var cursorRow *topicPostQueryRow
	if hasMore {
		if len(returnedRows) > 0 {
			row := returnedRows[len(returnedRows)-1]
			cursorRow = &row
		} else if len(rows) > 0 {
			row := rows[len(rows)-1]
			cursorRow = &row
		}
	}
	if cursorRow != nil {
		encoded, err := encodeTopicPostCursor(topicPostCursorV2{
			Version:    topicPostCursorVersion,
			TopicHash:  traversal.TopicHash,
			AnchorAt:   traversal.AnchorAt,
			Seed:       traversal.Seed,
			Bucket:     cursorRow.Bucket,
			ShuffleKey: cursorRow.ShuffleKey,
			PostID:     cursorRow.ID,
		})
		if err != nil {
			return postPageResponse{}, fmt.Errorf("encode topic cursor: %w", err)
		}
		page.NextCursor = &encoded
	}
	return page, nil
}

func loadTopicTraversalCandidates(db *gorm.DB, topic config.CuratedTopic, limit int, traversal topicTraversal) ([]topicPostQueryRow, error) {
	rows := make([]topicPostQueryRow, 0, limit)
	remaining := limit
	bucket := traversal.Bucket
	continuation := traversal.After
	for remaining > 0 {
		batch, err := loadTopicBucketCandidates(db, topic, traversal.AnchorAt, traversal.Seed, bucket, continuation, remaining)
		if err != nil {
			return nil, err
		}
		for index := range batch {
			batch[index].Bucket = bucket
		}
		rows = append(rows, batch...)
		remaining -= len(batch)
		if remaining == 0 {
			break
		}

		nextBucket, found, err := loadNextTopicBucket(db, topic, traversal.AnchorAt, bucket)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		if nextBucket <= bucket {
			return nil, errors.New("topic bucket traversal did not advance")
		}
		bucket = nextBucket
		continuation = nil
	}
	return rows, nil
}

func loadTopicBucketCandidates(db *gorm.DB, topic config.CuratedTopic, anchorAt time.Time, seed int64, bucket int64, after *topicPostCursorV2, limit int) ([]topicPostQueryRow, error) {
	query, args, err := topicBucketCandidatesQuery(topic, anchorAt, seed, bucket, after, limit)
	if err != nil {
		return nil, err
	}
	var rows []topicPostQueryRow
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func topicBucketCandidatesQuery(topic config.CuratedTopic, anchorAt time.Time, seed int64, bucket int64, after *topicPostCursorV2, limit int) (string, []interface{}, error) {
	if limit <= 0 {
		return "", nil, errors.New("invalid limit")
	}
	bucketStart, bucketEnd, err := topicBucketRange(anchorAt, bucket)
	if err != nil {
		return "", nil, err
	}
	// The 12-hour range scopes the seeded sort; imported_at excludes late historical imports.
	query := `
SELECT id, shuffle_key
FROM (
    SELECT mirror_posts.local_post_id AS id,
           hashint8extended(mirror_posts.local_post_id::bigint, ?::bigint) AS shuffle_key
    FROM devdata_mirror_posts AS mirror_posts
    JOIN devdata_mirror_accounts AS mirror_accounts
      ON mirror_accounts.id = mirror_posts.mirror_account_id
    JOIN posts
      ON posts.id = mirror_posts.local_post_id
    WHERE mirror_accounts.registry_key IN ?
      AND mirror_accounts.enabled = TRUE
      AND mirror_posts.state = ?
      AND mirror_posts.imported_at <= ?
      AND mirror_posts.source_created_at > ?
      AND mirror_posts.source_created_at <= ?
      AND ` + publicPostEligibilitySQL("posts") + `
) AS candidates
`
	args := []interface{}{seed, topic.SourceKeys, models.DevDataMirrorPostStateActive, anchorAt, bucketStart, bucketEnd}
	if after != nil {
		query += `WHERE shuffle_key < ? OR (shuffle_key = ? AND id < ?)
`
		args = append(args, after.ShuffleKey, after.ShuffleKey, after.PostID)
	}
	query += `ORDER BY shuffle_key DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return query, args, nil
}

func loadNextTopicBucket(db *gorm.DB, topic config.CuratedTopic, anchorAt time.Time, bucket int64) (int64, bool, error) {
	query, args, err := topicNextBucketLookupQuery(topic, anchorAt, bucket)
	if err != nil {
		return 0, false, err
	}
	var sourceTime sql.NullTime
	if err := db.Raw(query, args...).Row().Scan(&sourceTime); err != nil {
		return 0, false, err
	}
	if !sourceTime.Valid {
		return 0, false, nil
	}
	nextBucket, err := topicBucketIndex(anchorAt, sourceTime.Time)
	if err != nil {
		return 0, false, err
	}
	return nextBucket, true, nil
}

func topicNextBucketLookupQuery(topic config.CuratedTopic, anchorAt time.Time, bucket int64) (string, []interface{}, error) {
	bucketStart, _, err := topicBucketRange(anchorAt, bucket)
	if err != nil {
		return "", nil, err
	}
	// Jump to the next occupied bucket instead of querying every empty twelve-hour interval.
	query := `
SELECT MAX(mirror_posts.source_created_at)
FROM devdata_mirror_posts AS mirror_posts
JOIN devdata_mirror_accounts AS mirror_accounts
  ON mirror_accounts.id = mirror_posts.mirror_account_id
JOIN posts
  ON posts.id = mirror_posts.local_post_id
WHERE mirror_accounts.registry_key IN ?
  AND mirror_accounts.enabled = TRUE
  AND mirror_posts.state = ?
  AND mirror_posts.imported_at <= ?
  AND mirror_posts.source_created_at <= ?
  AND ` + publicPostEligibilitySQL("posts")
	return query, []interface{}{topic.SourceKeys, models.DevDataMirrorPostStateActive, anchorAt, bucketStart}, nil
}

func writeTopicInternalError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
