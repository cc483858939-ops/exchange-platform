package controllers

import (
	"errors"
	"net/http"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// createArticleRequest 只接收创建文章所需字段，避免客户端伪造 AI 结果字段。
type createArticleRequest struct {
	Title     string     `json:"title" binding:"required"`
	Content   string     `json:"content" binding:"required"`
	Preview   string     `json:"preview" binding:"required"`
	ExpiredAt *time.Time `json:"expired_at"`
}

var createArticleRecord = func(article *models.Article) error {
	return global.Db.Create(article).Error
}

var markArticleStatus = func(id uint, status string) error {
	return global.Db.Model(&models.Article{}).Where("id = ?", id).Update("status", status).Error
}

var invalidateArticleListCache = InvalidateArticleListCache

// enqueueArticleAnalysis 只负责把文章 ID 放进待分析集合，实际 AI 处理由后台 worker 完成。
var enqueueArticleAnalysis = func(articleID uint) error {
	return global.RedisDB.SAdd(consts.ArticleAnalysisDirtySetKey, articleID).Err()
}

var articleControllerLogError = func(ctx *gin.Context, msg string, err error) {
	if global.Db != nil {
		global.Db.Logger.Error(ctx, msg, err)
	}
}

func CreateArticle(ctx *gin.Context) {
	var req createArticleRequest
	if err := ctx.ShouldBind(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 文章创建时先落库，AI 相关字段统一由异步链路补齐。
	article := models.Article{
		Title:     req.Title,
		Content:   req.Content,
		Preview:   req.Preview,
		ExpiredAt: req.ExpiredAt,
		Status:    consts.ArticleStatusPending,
	}

	if err := createArticleRecord(&article); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := invalidateArticleListCache(); err != nil {
		articleControllerLogError(ctx, "failed to invalidate article list cache", err)
	}

	if err := enqueueArticleAnalysis(article.ID); err != nil {
		// 这里不回滚文章创建，只把状态标成 failed，方便后续排查或补偿。
		articleControllerLogError(ctx, "failed to enqueue article analysis", err)
		if updateErr := markArticleStatus(article.ID, consts.ArticleStatusFailed); updateErr != nil {
			articleControllerLogError(ctx, "failed to mark article analysis status as failed", updateErr)
		} else {
			article.Status = consts.ArticleStatusFailed
		}
	}

	ctx.JSON(http.StatusCreated, article)
}

func GetArticle(ctx *gin.Context) {
	articles, err := loadArticleList()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, articles)
}

func GetArticleByID(ctx *gin.Context) {
	article, err := loadArticleDetail(ctx.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, article)
}
