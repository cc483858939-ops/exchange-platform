package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/translation"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const postTranslationRequestMaxBytes int64 = 4 << 10

type postTranslationRequest struct {
	TargetLanguage string `json:"target_language"`
}

type postTranslationResponse struct {
	PostID         uint   `json:"post_id"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	Translated     bool   `json:"translated"`
	Translation    string `json:"translation"`
}

type postTranslationFields struct {
	ID       uint   `gorm:"column:id"`
	Content  string `gorm:"column:content"`
	Language string `gorm:"column:language"`
}

var loadPostTranslationFields = loadPostTranslationFieldsFromDB

func NewPostTranslationHandler(service translation.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		translatePost(ctx, service)
	}
}

func parsePostTranslationID(raw string) (uint, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, errors.New("invalid post id")
	}
	return uint(id), nil
}

func decodePostTranslationRequest(ctx *gin.Context) (postTranslationRequest, error) {
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return postTranslationRequest{}, errors.New("request body is required")
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, postTranslationRequestMaxBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	var request postTranslationRequest
	if err := decoder.Decode(&request); err != nil {
		return postTranslationRequest{}, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return postTranslationRequest{}, errors.New("multiple JSON values")
		}
		return postTranslationRequest{}, err
	}
	return request, nil
}

func loadPostTranslationFieldsFromDB(db *gorm.DB, postID uint) (postTranslationFields, error) {
	if db == nil {
		return postTranslationFields{}, errors.New("database is not initialized")
	}
	var post postTranslationFields
	err := publicPostScope(
		db.Model(&models.Post{}).Select("posts.id,posts.content,posts.language"),
		time.Now().UTC(),
	).Where("posts.id = ?", postID).First(&post).Error
	if err != nil {
		return postTranslationFields{}, err
	}
	return post, nil
}

func translatePost(ctx *gin.Context, service translation.Service) {
	if _, ok := userIDFromContext(ctx); !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	postID, err := parsePostTranslationID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid post id"})
		return
	}
	request, err := decodePostTranslationRequest(ctx)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "translation request body too large"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid translation request"})
		return
	}
	targetLanguage, ok := translation.NormalizeTargetLanguage(request.TargetLanguage)
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid target language"})
		return
	}
	post, err := loadPostTranslationFields(global.Db, postID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
			return
		}
		log.Printf("[PostTranslation] load post %d: %v", postID, err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Keep the same-language path entirely local. It must not touch the
	// provider or create a derived cache entry.
	if post.Language == targetLanguage {
		ctx.JSON(http.StatusOK, postTranslationResponse{
			PostID: post.ID, SourceLanguage: post.Language, TargetLanguage: targetLanguage,
			Translated: false, Translation: post.Content,
		})
		return
	}
	if service == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "translation is unavailable"})
		return
	}

	result, err := service.Translate(
		ctx.Request.Context(), post.ID, post.Content, post.Language, targetLanguage,
	)
	if err != nil {
		writePostTranslationError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, postTranslationResponse{
		PostID: post.ID, SourceLanguage: result.SourceLanguage, TargetLanguage: result.TargetLanguage,
		Translated: result.Translated, Translation: result.Translation,
	})
}

func writePostTranslationError(ctx *gin.Context, err error) {
	var providerError *translation.ProviderError
	switch {
	case errors.Is(err, translation.ErrSourceTooLong):
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": "post content is too long to translate"})
	case errors.Is(err, translation.ErrProviderRateLimited):
		if errors.As(err, &providerError) && providerError.RetryAfterHeader != "" {
			ctx.Header("Retry-After", providerError.RetryAfterHeader)
		}
		ctx.JSON(http.StatusTooManyRequests, gin.H{"error": "translation provider rate limit reached"})
	case errors.Is(err, translation.ErrFeatureDisabled),
		errors.Is(err, translation.ErrProviderTimeout),
		errors.Is(err, translation.ErrProviderUnavailable),
		errors.Is(err, translation.ErrProviderInvalidResponse),
		errors.Is(err, translation.ErrProviderMisconfigured):
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "translation is unavailable"})
	case errors.Is(err, translation.ErrInvalidTargetLanguage):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid target language"})
	default:
		log.Printf("[PostTranslation] service error: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
