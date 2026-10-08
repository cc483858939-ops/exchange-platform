package controllers

import (
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type quoteListResponse struct {
	Items      []postResponse `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func postQuotesPageQuery(db *gorm.DB, postID uint, now time.Time, cursor *postRelationCursor) *gorm.DB {
	query := publicPostScope(db.Model(&models.Post{}), now).
		Where("posts.quote_post_id = ?", postID)
	if cursor != nil {
		// A row comparison lets the ordered target index seek directly to the
		// cursor instead of scanning newer quotes and filtering them afterwards.
		query = query.Where("(posts.created_at, posts.id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}
	return query
}

func GetPostQuotes(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	limit, err := parsePostRelationLimit(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cursor, err := parsePostRelationCursor(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.APIDb == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}

	db := global.APIDb.WithContext(ctx.Request.Context())
	now := time.Now().UTC()
	var target models.Post
	if err := publicPostScope(db.Model(&models.Post{}).Select("id"), now).
		Where("posts.id = ?", postID).
		First(&target).Error; err != nil {
		writeReplyPostLookupError(ctx, err)
		return
	}

	query := postQuotesPageQuery(db, postID, now, cursor)
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
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := hydratePostResponsesMediaFromDB(db, items); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := hydratePostResponseRepostCountsFromDB(db, items); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := hydratePostResponsesReferencesFromDB(db, items, now); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var nextCursor *string
	if hasMore {
		last := posts[len(posts)-1]
		encoded, err := encodePostRelationCursor(postRelationCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		nextCursor = &encoded
	}
	ctx.JSON(http.StatusOK, quoteListResponse{Items: items, NextCursor: nextCursor})
}
