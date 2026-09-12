package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	maxClientPublishKeyLength = 128
	clientPublishUniqueIndex  = "uidx_posts_author_client_publish_id"
	clientPublishConflictCode = "POST_IDEMPOTENCY_CONFLICT"
	clientPublishConflictText = "Idempotency-Key was already used with a different post payload."
	invalidClientPublishCode  = "POST_INVALID_IDEMPOTENCY_KEY"
)

var errInvalidClientPublishKey = errors.New("invalid Idempotency-Key")

type createPostFingerprintMedia struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type createPostFingerprintPayload struct {
	Content       string                       `json:"content"`
	ReplyToPostID *uint                        `json:"reply_to_post_id"`
	QuotePostID   *uint                        `json:"quote_post_id"`
	Media         []createPostFingerprintMedia `json:"media"`
}

func parseClientPublishKey(ctx *gin.Context) (*uuid.UUID, bool, error) {
	if ctx == nil || ctx.Request == nil {
		return nil, false, nil
	}
	values := ctx.Request.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return nil, false, nil
	}
	if len(values) != 1 {
		return nil, true, errInvalidClientPublishKey
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" || len(raw) > maxClientPublishKeyLength {
		return nil, true, errInvalidClientPublishKey
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, true, errInvalidClientPublishKey
	}
	return &parsed, true, nil
}

func createPostPayloadFingerprint(content string, req createPostRequest) (string, error) {
	media := make([]createPostFingerprintMedia, 0, len(req.Media))
	for _, item := range req.Media {
		media = append(media, createPostFingerprintMedia{
			Type: strings.TrimSpace(item.Type),
			URL:  strings.TrimSpace(item.URL),
		})
	}
	payload := createPostFingerprintPayload{
		Content:       strings.TrimSpace(content),
		ReplyToPostID: req.ReplyToPostID,
		QuotePostID:   req.QuotePostID,
		Media:         media,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

var loadClientPublishPostFn = loadClientPublishPost

func loadClientPublishPost(authorID uint, clientPublishID uuid.UUID) (models.Post, error) {
	if global.Db == nil {
		return models.Post{}, errors.New("database is not initialized")
	}
	var post models.Post
	query := global.Db.Unscoped().Model(&models.Post{}).
		Where("posts.author_id = ? AND posts.client_publish_id = ?", authorID, clientPublishID)
	if err := preloadPostAuthor(query).First(&post).Error; err != nil {
		return models.Post{}, err
	}
	return post, nil
}

func buildPostCreateResponse(post models.Post, media []postMediaResponse, now time.Time) (postResponse, error) {
	response, err := postResponseFromModel(post)
	if err != nil {
		return postResponse{}, err
	}
	response.Media = media
	ensurePostResponseMedia(&response)
	if err := hydratePostResponseReferences(&response, now); err != nil {
		return postResponse{}, err
	}
	return response, nil
}

func buildStoredPostCreateResponse(post models.Post, now time.Time) (postResponse, error) {
	if global.Db == nil {
		return postResponse{}, errors.New("database is not initialized")
	}
	response, err := postResponseFromModel(post)
	if err != nil {
		return postResponse{}, err
	}
	if err := hydratePostResponseMediaFromDB(global.Db, &response); err != nil {
		return postResponse{}, err
	}
	if err := hydratePostResponseReferences(&response, now); err != nil {
		return postResponse{}, err
	}
	return response, nil
}

var buildStoredPostCreateResponseFn = buildStoredPostCreateResponse

func respondToExistingClientPublish(ctx *gin.Context, post models.Post, fingerprint string, now time.Time) bool {
	if post.DeletedAt.Valid || post.ClientPublishFingerprint == nil ||
		*post.ClientPublishFingerprint != fingerprint {
		ctx.JSON(http.StatusConflict, gin.H{
			"code":  clientPublishConflictCode,
			"error": clientPublishConflictText,
		})
		return true
	}
	response, err := buildStoredPostCreateResponseFn(post, now)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return true
	}
	ctx.Header("Idempotency-Replayed", "true")
	ctx.JSON(http.StatusOK, response)
	return true
}

func isClientPublishUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	// Some database wrappers omit ConstraintName. In that case the caller
	// still requires a successful exact-key lookup before treating the error
	// as an idempotency replay.
	return pgErr.ConstraintName == "" || pgErr.ConstraintName == clientPublishUniqueIndex
}
