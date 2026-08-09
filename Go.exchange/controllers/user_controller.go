package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultUserArticleLimit = 20
	maxUserArticleLimit     = 50
)

func parsePublicUserID(raw string) (uint, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 0)
	if err != nil || id == 0 {
		return 0, errors.New("invalid user id")
	}
	return uint(id), nil
}

func parseUserArticlePagination(ctx *gin.Context) (int, int, error) {
	limit := defaultUserArticleLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxUserArticleLimit {
		limit = maxUserArticleLimit
	}

	offset := 0
	if raw, exists := ctx.GetQuery("offset"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("invalid offset")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func writeUserAPIError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func GetUserByID(ctx *gin.Context) {
	id, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := loadPublicUserByID(id)
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, user)
}

func GetUserArticles(ctx *gin.Context) {
	id, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, offset, err := parseUserArticlePagination(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := loadPublicUserByID(id); err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	if global.Db == nil {
		writeUserAPIError(ctx, errors.New("database is not initialized"))
		return
	}
	query := global.Db.
		Select(articleListSelectColumns).
		Where("author_id = ?", id).
		Scopes(func(tx *gorm.DB) *gorm.DB { return visibleArticleScope(tx, time.Now()) }).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset)
	articles, err := loadArticleResponses(query)
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, articles)
}
