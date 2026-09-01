package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	replyRequestMaxBytes = 16 * 1024
	defaultReplyLimit    = 20
	maxReplyLimit        = 50
	maxReplyContentRunes = 1000
)

type createReplyRequest struct {
	Content string `json:"content" binding:"required"`
}

var (
	loadPostAuthorForReply = loadPublicAuthorByID
	createReplyWithCountFn = createReplyWithCount
)

type replyCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint      `json:"id"`
}

func CreatePostReply(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if ctx.Request == nil || ctx.Request.Body == nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request data"})
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, replyRequestMaxBytes)
	var req createReplyRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "reply request body is too large"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request data"})
		return
	}

	content, err := normalizeReplyContent(req.Content)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	author, err := loadPostAuthorForReply(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	reply, err := createReplyWithCountFn(postID, userID, content)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeReplyPostLookupError(ctx, err)
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	initializePostLikeStateAfterCommit(ctx, reply.ID)
	invalidateReplyPostCaches(postID)
	reply.Author = models.User{
		Model:       gorm.Model{ID: author.ID},
		Username:    author.Username,
		DisplayName: author.DisplayName,
		AvatarURL:   author.AvatarURL,
	}

	response, err := newReplyResponse(reply)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, response)
}

func GetPostReplies(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	limit, err := parseReplyLimit(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cursor, err := parseReplyCursor(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}

	var post models.Post
	if err := global.Db.
		Select("id").
		Scopes(func(tx *gorm.DB) *gorm.DB { return publicPostScope(tx, time.Now().UTC()) }).
		First(&post, postID).Error; err != nil {
		writeReplyPostLookupError(ctx, err)
		return
	}

	query := publicPostScope(global.Db.Model(&models.Post{}), time.Now().UTC()).Where("posts.reply_to_post_id = ?", postID)
	if cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}

	posts := make([]models.Post, 0, limit+1)
	if err := query.
		Preload("Author", func(tx *gorm.DB) *gorm.DB {
			return tx.Select("id, username, display_name, avatar_url")
		}).
		Order("created_at DESC, id DESC").
		Limit(limit + 1).
		Find(&posts).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	items := make([]replyResponse, 0, len(posts))
	for _, reply := range posts {
		response, err := newReplyResponse(reply)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items = append(items, response)
	}

	var nextCursor *string
	if hasMore {
		last := posts[len(posts)-1]
		encoded, err := encodeReplyCursor(replyCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		nextCursor = &encoded
	}
	ctx.JSON(http.StatusOK, replyListResponse{Items: items, NextCursor: nextCursor})
}

func DeletePostReply(ctx *gin.Context) {
	replyID, ok := replyIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	reply, err := deleteReplyWithCount(replyID, userID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		case errors.Is(err, errReplyForbidden):
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		default:
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if reply.ReplyToPostID != nil {
		invalidateReplyPostCaches(*reply.ReplyToPostID)
	}
	ctx.Status(http.StatusNoContent)
	ctx.Writer.WriteHeaderNow()
}

func replyIDFromContext(ctx *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid post id"})
		return 0, false
	}
	return uint(id), true
}

func normalizeReplyContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("post content is required")
	}
	if utf8.RuneCountInString(content) > maxReplyContentRunes {
		return "", errors.New("post content must not exceed 1000 characters")
	}
	return content, nil
}

func parseReplyLimit(ctx *gin.Context) (int, error) {
	limit := defaultReplyLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxReplyLimit {
		limit = maxReplyLimit
	}
	return limit, nil
}

func parseReplyCursor(ctx *gin.Context) (*replyCursor, error) {
	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return nil, nil
	}
	cursor, err := decodeReplyCursor(raw)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func encodeReplyCursor(cursor replyCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeReplyCursor(raw string) (replyCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return replyCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return replyCursor{}, errors.New("invalid cursor")
	}
	var cursor replyCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return replyCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeReplyPostLookupError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
