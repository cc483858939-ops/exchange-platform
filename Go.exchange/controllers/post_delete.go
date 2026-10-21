package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"
	"Go.exchange/postmediacleanup"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errPostDeleteNotFound    = errors.New("post not found")
	errPostDeleteForbidden   = errors.New("forbidden")
	errPostDeleteConsistency = errors.New("post delete consistency error")

	loadPostDeleteViewer = loadActiveFollowingViewerFromDB

	deletePostInTransaction        = deletePostInTransactionFromDB
	deletePostRepostsInTransaction = func(tx *gorm.DB, postID uint) error {
		return tx.Where("post_id = ?", postID).Delete(&models.PostRepost{}).Error
	}

	invalidatePostDeleteDetailCache = func(postID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidatePostDetailCacheByID(postID)
	}
	cleanupDeletedPostLikeState = func(postID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return likes.NewStore(global.RedisDB).DeletePost(ctx, postID)
	}
)

type postDeleteResult struct {
	ParentPostID      *uint
	QuoteTargetPostID *uint
}

func parsePostDeleteID(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, errors.New("invalid post id")
	}
	return uint(id), nil
}

func deletePostInTransactionFromDB(ctx context.Context, postID, viewerID uint) (postDeleteResult, error) {
	var deleteResult postDeleteResult
	if global.APIDb == nil {
		return deleteResult, errors.New("database is not initialized")
	}

	err := global.APIDb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var post models.Post
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&post, postID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errPostDeleteNotFound
			}
			return err
		}
		if post.AuthorID != viewerID {
			return errPostDeleteForbidden
		}
		if post.ReplyToPostID != nil {
			// Serialize against concurrent replies/deletes of the direct parent.
			var parent models.Post
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, *post.ReplyToPostID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errPostDeleteConsistency
				}
				return err
			}
			rowsAffected, err := decrementPostReplyCount(tx, parent.ID)
			if err != nil {
				return err
			}
			if rowsAffected != 1 {
				return errPostDeleteConsistency
			}
			parentID := parent.ID
			deleteResult.ParentPostID = &parentID
		}
		if post.QuotePostID != nil {
			var target models.Post
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, *post.QuotePostID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errPostQuoteCountConsistency
				}
				return err
			}
			rowsAffected, err := decrementPostQuoteCount(tx, target.ID)
			if err != nil {
				return err
			}
			if rowsAffected != 1 {
				return errPostQuoteCountConsistency
			}
			targetID := target.ID
			deleteResult.QuoteTargetPostID = &targetID
		}

		if err := deletePostRepostsInTransaction(tx, postID); err != nil {
			return err
		}
		result := tx.Delete(&post)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errPostDeleteConsistency
		}
		if err := tx.Create(&models.PostLikeCleanup{PostID: postID, RetryAfter: time.Now().UTC()}).Error; err != nil {
			return err
		}
		return postmediacleanup.EnqueueDeletedPost(tx, postID, viewerID, time.Now())
	})
	if err != nil {
		return postDeleteResult{}, err
	}
	return deleteResult, nil
}

func writePostDeleteStoreError(ctx *gin.Context, err error) {
	writeInternalAPIError(ctx, err)
}

func DeletePost(ctx *gin.Context) {
	postID, err := parsePostDeleteID(ctx.Param("id"))
	if err != nil {
		writeAPIError(ctx, "INVALID_REQUEST")
		return
	}

	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		writeAPIError(ctx, "UNAUTHORIZED")
		return
	}
	if err := loadPostDeleteViewer(ctx.Request.Context(), viewerID); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(ctx, "UNAUTHORIZED")
			return
		}
		writePostDeleteStoreError(ctx, err)
		return
	}

	deleteResult, err := deletePostInTransaction(ctx.Request.Context(), postID, viewerID)
	if err != nil {
		switch {
		case errors.Is(err, errPostDeleteNotFound):
			writeAPIError(ctx, "POST_NOT_FOUND")
		case errors.Is(err, errPostDeleteForbidden):
			writeAPIError(ctx, "FORBIDDEN")
		default:
			writePostDeleteStoreError(ctx, err)
		}
		return
	}

	if err := invalidatePostDeleteDetailCache(postID); err != nil {
		logAPIInternalError(ctx, http.StatusNoContent, err)
	}
	if deleteResult.ParentPostID != nil {
		if err := invalidatePostDeleteDetailCache(*deleteResult.ParentPostID); err != nil {
			logAPIInternalError(ctx, http.StatusNoContent, err)
		}
	}
	if deleteResult.QuoteTargetPostID != nil {
		if err := invalidatePostDeleteDetailCache(*deleteResult.QuoteTargetPostID); err != nil {
			logAPIInternalError(ctx, http.StatusNoContent, err)
		}
	}
	if err := cleanupDeletedPostLikeState(postID); err != nil {
		logAPIInternalError(ctx, http.StatusNoContent, err)
	}

	ctx.Status(http.StatusNoContent)
	ctx.Writer.WriteHeaderNow()
}
