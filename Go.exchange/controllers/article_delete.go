package controllers

import (
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
	errPostDeleteNotFound  = errors.New("post not found")
	errPostDeleteForbidden = errors.New("forbidden")

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

		pipeline := global.RedisDB.Pipeline()
		pipeline.Del(likes.ReadyKey(postID))
		pipeline.SRem(likes.DirtyKey, postID)
		pipeline.ZRem(likes.ProcessingKey, postID)
		pipeline.HDel(likes.ClaimsKey, strconv.FormatUint(uint64(postID), 10))
		_, err := pipeline.Exec()
		return err
	}
)

func parsePostDeleteID(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, errors.New("invalid post id")
	}
	return uint(id), nil
}

func deletePostInTransactionFromDB(postID, viewerID uint) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	return global.Db.Transaction(func(tx *gorm.DB) error {
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
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&models.Post{}, *post.ReplyToPostID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			result := tx.Model(&models.Post{}).
				Where("id = ? AND reply_count > 0", *post.ReplyToPostID).
				UpdateColumn("reply_count", gorm.Expr("reply_count - 1"))
			if result.Error != nil {
				return result.Error
			}
		}

		if err := deletePostRepostsInTransaction(tx, postID); err != nil {
			return err
		}
		if err := tx.Delete(&post).Error; err != nil {
			return err
		}
		return nil
	})
}

func writePostDeleteStoreError(ctx *gin.Context) {
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
	if err := loadPostDeleteViewer(viewerID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		writePostDeleteStoreError(ctx)
		return
	}

	if err := deletePostInTransaction(postID, viewerID); err != nil {
		switch {
		case errors.Is(err, errPostDeleteNotFound):
			ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		case errors.Is(err, errPostDeleteForbidden):
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		default:
			writePostDeleteStoreError(ctx)
		}
		return
	}

	if err := invalidatePostDeleteDetailCache(postID); err != nil {
		log.Printf("[PostDelete] invalidate post detail cache for %d: %v", postID, err)
	}
	if err := cleanupDeletedPostLikeState(postID); err != nil {
		log.Printf("[PostDelete] clean up like state for %d: %v", postID, err)
	}

	ctx.Status(http.StatusNoContent)
}
