package controllers

import (
	"errors"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type replyCursor = postRelationCursor

func GetPostReplies(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	limit, err := parseReplyLimit(ctx)
	if err != nil {
		writeAPIError(ctx, "INVALID_REQUEST")
		return
	}
	cursor, err := parseReplyCursor(ctx)
	if err != nil {
		writeAPIError(ctx, "INVALID_REQUEST")
		return
	}
	if global.APIDb == nil {
		writeInternalAPIErrorCode(ctx, "INTERNAL_ERROR")
		return
	}

	db := global.APIDb.WithContext(ctx.Request.Context())
	var post models.Post
	if err := db.
		Select("id").
		Scopes(func(tx *gorm.DB) *gorm.DB { return publicPostScope(tx, time.Now().UTC()) }).
		First(&post, postID).Error; err != nil {
		writeReplyPostLookupError(ctx, err)
		return
	}

	query := publicPostScope(db.Model(&models.Post{}), time.Now().UTC()).Where("posts.reply_to_post_id = ?", postID)
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
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}

	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	items, err := newPostResponses(posts)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	if err := hydratePostResponsesMediaFromDB(db, items); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	if err := hydratePostResponseRepostCountsFromDB(db, items); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	referenceNow := time.Now().UTC()
	if err := hydratePostResponsesReferencesFromDB(db, items, referenceNow); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}

	var nextCursor *string
	if hasMore {
		last := posts[len(posts)-1]
		encoded, err := encodeReplyCursor(replyCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			writeInternalAPIError(ctx, err)
			return
		}
		nextCursor = &encoded
	}
	ctx.JSON(http.StatusOK, replyListResponse{Items: items, NextCursor: nextCursor})
}

func writeReplyPostLookupError(ctx *gin.Context, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeAPIError(ctx, "POST_NOT_FOUND")
		return
	}
	writeInternalAPIError(ctx, err)
}
