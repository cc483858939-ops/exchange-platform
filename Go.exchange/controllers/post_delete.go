package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

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
		return likes.NewStore(global.RedisDB).PurgePost(context.Background(), postID)
	}
)

type postDeleteResult struct {
	ParentPostID *uint
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
	if global.Db == nil {
		return deleteResult, errors.New("database is not initialized")
	}

	err := global.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		return nil
	})
	if err != nil {
		return postDeleteResult{}, err
	}
	return deleteResult, nil
}

func writePostDeleteStoreError(ctx *gin.Context, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func DeletePost(ctx *gin.Context) {
	postID, err := parsePostDeleteID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if err := loadPostDeleteViewer(ctx.Request.Context(), viewerID); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		writePostDeleteStoreError(ctx, err)
		return
	}

	deleteResult, err := deletePostInTransaction(ctx.Request.Context(), postID, viewerID)
	if err != nil {
		switch {
		case errors.Is(err, errPostDeleteNotFound):
			ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		case errors.Is(err, errPostDeleteForbidden):
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		default:
			writePostDeleteStoreError(ctx, err)
		}
		return
	}

	if err := invalidatePostDeleteDetailCache(postID); err != nil {
		log.Printf("[PostDelete] invalidate post detail cache for %d: %v", postID, err)
	}
	if deleteResult.ParentPostID != nil {
		if err := invalidatePostDeleteDetailCache(*deleteResult.ParentPostID); err != nil {
			log.Printf("[PostDelete] invalidate parent post detail cache for %d: %v", *deleteResult.ParentPostID, err)
		}
	}
	if err := cleanupDeletedPostLikeState(postID); err != nil {
		log.Printf("[PostDelete] clean up like state for %d: %v", postID, err)
	}

	ctx.Status(http.StatusNoContent)
	ctx.Writer.WriteHeaderNow()
}
