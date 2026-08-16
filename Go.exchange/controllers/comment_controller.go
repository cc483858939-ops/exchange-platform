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
	commentRequestMaxBytes = 16 * 1024
	defaultCommentLimit    = 20
	maxCommentLimit        = 50
	maxCommentContentRunes = 1000
)

type createCommentRequest struct {
	Content string `json:"content" binding:"required"`
}

type commentCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint      `json:"id"`
}

func CreateArticleComment(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
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

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, commentRequestMaxBytes)
	var req createCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "comment request body is too large"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request data"})
		return
	}

	content, err := normalizeCommentContent(req.Content)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	author, err := loadPublicAuthorByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	comment, err := createCommentWithCount(articleID, userID, content)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeCommentArticleLookupError(ctx, err)
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	invalidateCommentArticleCaches(articleID)
	comment.Author = models.User{
		Model:       gorm.Model{ID: author.ID},
		Username:    author.Username,
		DisplayName: author.DisplayName,
		AvatarURL:   author.AvatarURL,
	}

	response, err := newCommentResponse(comment)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, response)
}

func GetArticleComments(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}
	limit, err := parseCommentLimit(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cursor, err := parseCommentCursor(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}

	var article models.Article
	if err := global.Db.
		Select("id").
		Scopes(func(tx *gorm.DB) *gorm.DB { return publicArticleScope(tx, time.Now().UTC()) }).
		First(&article, articleID).Error; err != nil {
		writeCommentArticleLookupError(ctx, err)
		return
	}

	query := global.Db.Where("article_id = ?", articleID)
	if cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}

	comments := make([]models.Comment, 0, limit+1)
	if err := query.
		Preload("Author", func(tx *gorm.DB) *gorm.DB {
			return tx.Select("id, username, display_name, avatar_url")
		}).
		Order("created_at DESC, id DESC").
		Limit(limit + 1).
		Find(&comments).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hasMore := len(comments) > limit
	if hasMore {
		comments = comments[:limit]
	}
	items := make([]commentResponse, 0, len(comments))
	for _, comment := range comments {
		response, err := newCommentResponse(comment)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items = append(items, response)
	}

	var nextCursor *string
	if hasMore {
		last := comments[len(comments)-1]
		encoded, err := encodeCommentCursor(commentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		nextCursor = &encoded
	}
	ctx.JSON(http.StatusOK, commentListResponse{Items: items, NextCursor: nextCursor})
}

func DeleteComment(ctx *gin.Context) {
	commentID, ok := commentIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	comment, err := deleteCommentWithCount(commentID, userID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			ctx.JSON(http.StatusNotFound, gin.H{"error": "comment not found"})
		case errors.Is(err, errCommentForbidden):
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		default:
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	invalidateCommentArticleCaches(comment.ArticleID)
	ctx.Status(http.StatusNoContent)
	ctx.Writer.WriteHeaderNow()
}

func commentIDFromContext(ctx *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment id"})
		return 0, false
	}
	return uint(id), true
}

func normalizeCommentContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("comment content is required")
	}
	if utf8.RuneCountInString(content) > maxCommentContentRunes {
		return "", errors.New("comment content must not exceed 1000 characters")
	}
	return content, nil
}

func parseCommentLimit(ctx *gin.Context) (int, error) {
	limit := defaultCommentLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxCommentLimit {
		limit = maxCommentLimit
	}
	return limit, nil
}

func parseCommentCursor(ctx *gin.Context) (*commentCursor, error) {
	raw, exists := ctx.GetQuery("cursor")
	if !exists {
		return nil, nil
	}
	cursor, err := decodeCommentCursor(raw)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func encodeCommentCursor(cursor commentCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCommentCursor(raw string) (commentCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return commentCursor{}, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return commentCursor{}, errors.New("invalid cursor")
	}
	var cursor commentCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return commentCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeCommentArticleLookupError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "article not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
