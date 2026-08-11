package controllers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errArticleDeleteNotFound  = errors.New("article not found")
	errArticleDeleteForbidden = errors.New("forbidden")

	loadArticleDeleteViewer = loadActiveFollowingViewerFromDB

	deleteArticleInTransaction = deleteArticleInTransactionFromDB

	invalidateArticleDeleteListCache = func() error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidateArticleListCache()
	}
	invalidateArticleDeleteDetailCache = func(articleID uint) error {
		if global.RedisDB == nil {
			return nil
		}
		return InvalidateArticleDetailCacheByID(articleID)
	}
	cleanupDeletedArticleLikeState = func(articleID uint) error {
		if global.RedisDB == nil {
			return nil
		}

		pipeline := global.RedisDB.Pipeline()
		pipeline.Del(likes.ReadyKey(articleID))
		pipeline.SRem(likes.DirtyKey, articleID)
		pipeline.ZRem(likes.ProcessingKey, articleID)
		pipeline.HDel(likes.ClaimsKey, strconv.FormatUint(uint64(articleID), 10))
		_, err := pipeline.Exec()
		return err
	}
)

func parseArticleDeleteID(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, errors.New("invalid article id")
	}
	return uint(id), nil
}

func deleteArticleInTransactionFromDB(articleID, viewerID uint) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	return global.Db.Transaction(func(tx *gorm.DB) error {
		var analysisJob models.ArticleAnalysisJob
		jobResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("article_id = ?", articleID).
			First(&analysisJob)
		if jobResult.Error != nil && !errors.Is(jobResult.Error, gorm.ErrRecordNotFound) {
			return jobResult.Error
		}
		jobExists := jobResult.Error == nil

		var article models.Article
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&article, articleID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errArticleDeleteNotFound
			}
			return err
		}
		if article.AuthorID != viewerID {
			return errArticleDeleteForbidden
		}

		deletionTime := time.Now().UTC()
		if jobExists && (analysisJob.State == models.ArticleAnalysisJobQueued || analysisJob.State == models.ArticleAnalysisJobRetryWait) {
			if err := tx.Model(&analysisJob).Updates(map[string]interface{}{
				"state":       models.ArticleAnalysisJobCanceled,
				"lease_until": nil,
				"leased_by":   "",
				"finished_at": deletionTime,
				"last_error":  "article deleted",
			}).Error; err != nil {
				return err
			}
		}

		if err := tx.Delete(&article).Error; err != nil {
			return err
		}
		return nil
	})
}

func writeArticleDeleteStoreError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func DeleteArticle(ctx *gin.Context) {
	articleID, err := parseArticleDeleteID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	if err := loadArticleDeleteViewer(viewerID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		writeArticleDeleteStoreError(ctx)
		return
	}

	if err := deleteArticleInTransaction(articleID, viewerID); err != nil {
		switch {
		case errors.Is(err, errArticleDeleteNotFound):
			ctx.JSON(http.StatusNotFound, gin.H{"error": "article not found"})
		case errors.Is(err, errArticleDeleteForbidden):
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		default:
			writeArticleDeleteStoreError(ctx)
		}
		return
	}

	if err := invalidateArticleDeleteListCache(); err != nil {
		log.Printf("[ArticleDelete] invalidate article list cache: %v", err)
	}
	if err := invalidateArticleDeleteDetailCache(articleID); err != nil {
		log.Printf("[ArticleDelete] invalidate article detail cache for %d: %v", articleID, err)
	}
	if err := cleanupDeletedArticleLikeState(articleID); err != nil {
		log.Printf("[ArticleDelete] clean up like state for %d: %v", articleID, err)
	}

	ctx.Status(http.StatusNoContent)
}
