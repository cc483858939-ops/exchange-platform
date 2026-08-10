package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type createArticleRequest struct {
	Title         string     `json:"title"`
	Content       string     `json:"content"`
	Preview       string     `json:"preview"`
	ExpiredAt     *time.Time `json:"expired_at"`
	CoverImageURL string     `json:"cover_image_url"`
}

var createArticleWithAnalysisJob = func(article *models.Article) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(article).Error; err != nil {
			return err
		}

		now := time.Now().UTC()
		maxAttempts := config.AppConfig.Kafka.JobMaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}
		job := models.ArticleAnalysisJob{
			ArticleID:        article.ID,
			State:            models.ArticleAnalysisJobQueued,
			MaxAttempts:      maxAttempts,
			NextAttemptAt:    now,
			LastDispatchedAt: &now,
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		event, err := eventing.NewArticleAnalysisRequested(job, article.AnalysisVersion)
		if err != nil {
			return err
		}
		return eventing.AddOutboxEvent(tx, event)
	})
}

var invalidateArticleListCache = InvalidateArticleListCache
var loadArticleAuthorForCreate = loadPublicAuthorByID
var initializeArticleLikeState = func(articleID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	_, err := likes.NewStore(global.RedisDB).Initialize(context.Background(), articleID, 0, 0, nil)
	return err
}

func normalizeArticleCoverImageURL(raw string) (string, error) {
	coverImageURL := strings.TrimSpace(raw)
	if coverImageURL == "" {
		return "", nil
	}
	if !strings.HasPrefix(coverImageURL, "/api/files/article-covers/") {
		return "", errors.New("invalid cover_image_url")
	}
	if strings.Contains(coverImageURL, "..") || strings.ContainsAny(coverImageURL, "\r\n") {
		return "", errors.New("invalid cover_image_url")
	}
	return coverImageURL, nil
}

func CreateArticle(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	author, err := loadArticleAuthorForCreate(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req createArticleRequest
	if err := ctx.ShouldBind(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	title := strings.TrimSpace(req.Title)
	preview := strings.TrimSpace(req.Preview)
	content := strings.TrimSpace(req.Content)
	if content == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
		return
	}
	coverImageURL, err := normalizeArticleCoverImageURL(req.CoverImageURL)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	article := models.Article{
		AuthorID:         userID,
		Title:            title,
		Content:          content,
		Preview:          preview,
		ExpiredAt:        req.ExpiredAt,
		CoverImageURL:    coverImageURL,
		PublicationState: consts.ArticlePublicationStatePublished,
		AnalysisState:    consts.ArticleAnalysisStatePending,
		AnalysisVersion:  consts.ArticleAnalysisVersionV1,
		PublishedAt:      &now,
	}

	if err := createArticleWithAnalysisJob(&article); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := initializeArticleLikeState(article.ID); err != nil && global.Db != nil {
		global.Db.Logger.Error(ctx, "failed to initialize article like state", err)
	}
	if err := invalidateArticleListCache(); err != nil && global.Db != nil {
		global.Db.Logger.Error(ctx, "failed to invalidate article list cache", err)
	}

	article.Author = models.User{Model: gorm.Model{ID: author.ID}, Username: author.Username}
	response, err := newArticleResponse(article)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, response)
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
	id := ctx.Param("id")
	article, err := loadArticleDetail(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	recordArticleBehaviorFromContext(ctx, article.ID, ArticleBehaviorActionView)
	ctx.JSON(http.StatusOK, article)
}
