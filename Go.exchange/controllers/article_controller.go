package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type createArticleRequest struct {
	Title         string     `json:"title"`
	Content       string     `json:"content"`
	Preview       string     `json:"preview"`
	ExpiredAt     *time.Time `json:"expired_at"`
	CoverImageURL string     `json:"cover_image_url"`
}

var persistArticle = func(article *models.Article) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return global.Db.Create(article).Error
}

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

func NewCreateArticleHandler(publisher eventing.BatchPublisher) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		createArticle(ctx, publisher)
	}
}

func createArticle(ctx *gin.Context, publisher eventing.BatchPublisher) {
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
		PublishedAt:      &now,
	}

	if err := persistArticle(&article); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if config.AppConfig != nil && config.AppConfig.Embedding.Enabled {
		event, eventErr := eventing.NewArticleEmbeddingRequestedEnvelope(uuid.NewString(), article.ID, time.Now().UTC())
		if eventErr != nil {
			metrics.RecordArticleEmbeddingPublishFailure("article_create")
			log.Printf("[ArticleEmbedding] create event: %v", eventErr)
		} else if publisher == nil {
			metrics.RecordArticleEmbeddingPublishFailure("article_create")
			log.Printf("[ArticleEmbedding] article create publisher is unavailable for article %d", article.ID)
		} else if publishErr := publisher.PublishBatch(ctx.Request.Context(), []eventing.Envelope{event}); publishErr != nil {
			metrics.RecordArticleEmbeddingPublishFailure("article_create")
			log.Printf("[ArticleEmbedding] publish article %d request: %v", article.ID, publishErr)
		}
	}
	if err := initializeArticleLikeState(article.ID); err != nil && global.Db != nil {
		global.Db.Logger.Error(ctx, "failed to initialize article like state", err)
	}

	article.Author = models.User{
		Model:       gorm.Model{ID: author.ID},
		Username:    author.Username,
		DisplayName: author.DisplayName,
		AvatarURL:   author.AvatarURL,
	}
	response, err := newArticleResponse(article)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, response)
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
	ctx.JSON(http.StatusOK, article)
}
