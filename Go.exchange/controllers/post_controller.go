package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type createPostArticleRequest struct {
	Title         string     `json:"title"`
	Preview       string     `json:"preview"`
	CoverImageURL string     `json:"cover_image_url"`
	ExpiredAt     *time.Time `json:"expired_at"`
}

type createPostRequest struct {
	Content       string                    `json:"content"`
	ReplyToPostID *uint                     `json:"reply_to_post_id"`
	QuotePostID   *uint                     `json:"quote_post_id"`
	Article       *createPostArticleRequest `json:"article"`
}

const (
	createPostRequestMaxBytes = 1 << 20 // 1 MiB JSON request envelope
	maxReplyContentRunes      = 1000
)

var loadPostAuthorForCreate = loadPublicAuthorByID
var initializePostLikeState = func(postID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	_, err := likes.NewStore(global.RedisDB).Initialize(context.Background(), postID, 0, 0, nil)
	return err
}

var invalidatePostCreateParentDetailCache = func(postID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	return InvalidatePostDetailCacheByID(postID)
}

func initializePostLikeStateAfterCommit(ctx context.Context, postID uint) {
	if err := initializePostLikeState(postID); err != nil && global.Db != nil {
		global.Db.Logger.Error(ctx, "failed to initialize post like state", err)
	}
}

var persistPostGraphFn = persistPostGraph

func normalizePostCoverImageURL(raw string) (string, error) {
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

func NewCreatePostHandler(publisher eventing.BatchPublisher) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		createPost(ctx, publisher)
	}
}

func createPost(ctx *gin.Context, publisher eventing.BatchPublisher) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, createPostRequestMaxBytes)

	var req createPostRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
		return
	}
	if req.ReplyToPostID != nil && utf8.RuneCountInString(content) > maxReplyContentRunes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "reply content exceeds 1000 Unicode code points"})
		return
	}
	now := time.Now().UTC()
	if req.ReplyToPostID != nil && *req.ReplyToPostID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid reply_to_post_id"})
		return
	}
	if req.QuotePostID != nil && *req.QuotePostID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid quote_post_id"})
		return
	}
	if req.ReplyToPostID != nil && req.QuotePostID != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "a post cannot be both a reply and a quote"})
		return
	}
	if req.Article != nil && (req.ReplyToPostID != nil || req.QuotePostID != nil) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "article posts cannot be replies or quotes"})
		return
	}
	if req.Article != nil {
		if strings.TrimSpace(req.Article.Title) == "" || strings.TrimSpace(req.Article.Preview) == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "article title and preview are required"})
			return
		}
		if _, err := normalizePostCoverImageURL(req.Article.CoverImageURL); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	author, err := loadPostAuthorForCreate(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var post models.Post
	var article *models.PostArticle
	err = persistPostGraphFn(&post, &article, userID, content, req, now)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "reply or quote target unavailable"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if config.AppConfig != nil && config.AppConfig.Embedding.Enabled {
		event, eventErr := eventing.NewPostEmbeddingRequestedEnvelope(uuid.NewString(), post.ID, time.Now().UTC())
		if eventErr != nil {
			metrics.RecordPostEmbeddingPublishFailure("post_create")
			log.Printf("[PostEmbedding] create event: %v", eventErr)
		} else if publisher == nil {
			metrics.RecordPostEmbeddingPublishFailure("post_create")
			log.Printf("[PostEmbedding] post create publisher is unavailable for post %d", post.ID)
		} else if publishErr := publisher.PublishBatch(ctx.Request.Context(), []eventing.Envelope{event}); publishErr != nil {
			metrics.RecordPostEmbeddingPublishFailure("post_create")
			log.Printf("[PostEmbedding] publish post %d request: %v", post.ID, publishErr)
		}
	}
	initializePostLikeStateAfterCommit(ctx, post.ID)
	if post.ReplyToPostID != nil {
		if err := invalidatePostCreateParentDetailCache(*post.ReplyToPostID); err != nil {
			log.Printf("[PostCreate] invalidate parent post detail cache for %d: %v", *post.ReplyToPostID, err)
		}
	}

	post.Author = models.User{
		Model:       gorm.Model{ID: author.ID},
		Username:    author.Username,
		DisplayName: author.DisplayName,
		AvatarURL:   author.AvatarURL,
	}
	response, err := postResponseFromModel(post, article)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := hydratePostResponseReferences(&response, now); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, response)
}

func persistPostGraph(post *models.Post, article **models.PostArticle, userID uint, content string, req createPostRequest, now time.Time) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	return global.Db.Transaction(func(tx *gorm.DB) error {
		var parentAuthor uint
		*post = models.Post{
			AuthorID: userID, Content: content, ReplyToPostID: req.ReplyToPostID,
			QuotePostID: req.QuotePostID, Visibility: "public",
		}
		if req.ReplyToPostID != nil {
			var parent models.Post
			if err := publicPostScope(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("posts.id = ?", *req.ReplyToPostID), now).First(&parent).Error; err != nil {
				return err
			}
			rootID := parent.ID
			if parent.ConversationID != nil && *parent.ConversationID != 0 {
				rootID = *parent.ConversationID
			}
			parentAuthor = parent.AuthorID
			post.ConversationID = &rootID
		} else if req.QuotePostID != nil {
			var quoted models.Post
			if err := publicPostScope(tx.Where("posts.id = ?", *req.QuotePostID), now).First(&quoted).Error; err != nil {
				return err
			}
		}
		post.CreatedAt = now
		post.UpdatedAt = now
		if err := tx.Create(post).Error; err != nil {
			return err
		}
		if req.Article != nil {
			title := strings.TrimSpace(req.Article.Title)
			preview := strings.TrimSpace(req.Article.Preview)
			if title == "" || preview == "" {
				return errors.New("article title and preview are required")
			}
			cover, err := normalizePostCoverImageURL(req.Article.CoverImageURL)
			if err != nil {
				return err
			}
			row := &models.PostArticle{
				PostID: post.ID, Title: title, Preview: preview, CoverImageURL: cover,
				PublicationState: consts.PostPublicationStatePublished, PublishedAt: &now,
				ExpiredAt: req.Article.ExpiredAt,
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			*article = row
		}
		if post.ReplyToPostID != nil {
			rowsAffected, err := incrementPostReplyCount(tx, *post.ReplyToPostID)
			if err != nil {
				return err
			}
			if rowsAffected != 1 {
				return errPostReplyCountConsistency
			}
			rootID := *post.ConversationID
			if err := upsertReplyPostBehavior(tx, userID, rootID, now); err != nil {
				return err
			}
			if err := recommendation.InvalidateProfiles(tx, []uint{userID}, "reply_created", now); err != nil {
				return err
			}
			activity, err := eventing.NewReplyCreatedEnvelope(uuid.NewString(), eventing.ReplyCreatedPayload{
				ReplyPostID: post.ID, ParentPostID: *post.ReplyToPostID, ConversationID: rootID,
				ActorID: userID, ParentAuthorID: parentAuthor, CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if activity.Payload == nil {
				return errors.New("reply activity payload is empty")
			}
			if err := addConfiguredActivityOutboxEvent(tx, activity); err != nil {
				return err
			}
		}
		return nil
	})
}

func GetPostByID(ctx *gin.Context) {
	id := ctx.Param("id")
	article, err := loadPostDetail(id)
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
