package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/apierrors"
	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"
	"Go.exchange/postlanguage"
	"Go.exchange/postlikeinit"
	"Go.exchange/postmediaupload"
	"Go.exchange/postmention"
	"Go.exchange/ratelimit"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type createPostRequest struct {
	Content       string                   `json:"content"`
	ReplyToPostID *uint                    `json:"reply_to_post_id"`
	QuotePostID   *uint                    `json:"quote_post_id"`
	Media         []createPostMediaRequest `json:"media"`
	MentionTokens []string                 `json:"-"`

	ClientPublishID          *uuid.UUID `json:"-"`
	ClientPublishFingerprint *string    `json:"-"`
}

const (
	createPostRequestMaxBytes = 1 << 20 // 1 MiB JSON request envelope
	maxReplyContentRunes      = 1000
)

var errTooManyPostMentions = errors.New("a post can mention at most 5 different users")

var loadPostAuthorForCreate = loadPublicAuthorByID
var initializePostLikeState = func(ctx context.Context, postID uint) error {
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	return postlikeinit.RunNow(ctx, global.APIDb, likes.NewStore(global.RedisDB), postID)
}

var invalidatePostCreateParentDetailCache = func(postID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	return InvalidatePostDetailCacheByID(postID)
}

func initializePostLikeStateAfterCommit(ctx *gin.Context, postID uint) {
	if ctx == nil || ctx.Request == nil {
		return
	}
	if err := initializePostLikeState(ctx.Request.Context(), postID); err != nil {
		logAPIInternalError(ctx, http.StatusCreated, err)
	}
}

var persistPostGraphFn = persistPostGraph
var lockPendingPostMediaUploads = postmediaupload.LockForConsumption
var deleteConsumedPostMediaUploads = postmediaupload.DeleteConsumed

func NewCreatePostHandler(limiters ...ratelimit.Limiter) gin.HandlerFunc {
	var limiter ratelimit.Limiter
	if len(limiters) > 0 {
		limiter = limiters[0]
	}
	return NewCreatePostHandlerWithRateLimit(limiter, limiter != nil)
}

// NewCreatePostHandlerWithRateLimit preserves the idempotency-aware placement
// of the post quota while distinguishing an intentionally disabled limiter
// from an enabled limiter whose dependency is unavailable.
func NewCreatePostHandlerWithRateLimit(limiter ratelimit.Limiter, enabled bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		createPostWithRateLimiter(ctx, limiter, enabled)
	}
}

func createPost(ctx *gin.Context) {
	createPostWithRateLimiter(ctx, nil, false)
}

func createPostWithRateLimiter(ctx *gin.Context, limiter ratelimit.Limiter, enabled bool) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeAPIError(ctx, "UNAUTHORIZED")
		return
	}
	clientPublishID, hasClientPublishID, err := parseClientPublishKey(ctx)
	if err != nil {
		writeAPIError(ctx, invalidClientPublishCode)
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, createPostRequestMaxBytes)

	var req createPostRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(ctx, "PAYLOAD_TOO_LARGE")
			return
		}
		writeAPIError(ctx, "INVALID_REQUEST")
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeAPIError(ctx, "CONTENT_IS_REQUIRED")
		return
	}
	if req.ReplyToPostID != nil && utf8.RuneCountInString(content) > maxReplyContentRunes {
		writeAPIError(ctx, "REPLY_CONTENT_EXCEEDS_1000_UNICODE_CODE_POINTS")
		return
	}
	now := time.Now().UTC()
	if req.ReplyToPostID != nil && *req.ReplyToPostID == 0 {
		writeAPIError(ctx, "INVALID_REPLY_TO_POST_ID")
		return
	}
	if req.QuotePostID != nil && *req.QuotePostID == 0 {
		writeAPIError(ctx, "INVALID_QUOTE_POST_ID")
		return
	}
	if req.ReplyToPostID != nil && req.QuotePostID != nil {
		writeAPIError(ctx, "A_POST_CANNOT_BE_BOTH_A_REPLY_AND_A_QUOTE")
		return
	}
	var clientPublishFingerprint string
	if hasClientPublishID {
		clientPublishFingerprint, err = createPostPayloadFingerprint(content, req)
		if err != nil {
			writeInternalAPIError(ctx, err)
			return
		}
		req.ClientPublishID = clientPublishID
		req.ClientPublishFingerprint = &clientPublishFingerprint
		existing, lookupErr := loadClientPublishPostFn(ctx.Request.Context(), userID, *clientPublishID)
		if lookupErr == nil {
			respondToExistingClientPublish(ctx, existing, clientPublishFingerprint, now)
			return
		}
		if handleRequestDBError(ctx, lookupErr) {
			return
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			writeInternalAPIError(ctx, lookupErr)
			return
		}
	}
	mentionTokens, err := postmention.ExtractTokens(content)
	if errors.Is(err, postmention.ErrTooManyCandidates) {
		writeAPIError(ctx, "TOO_MANY_MENTION_CANDIDATES")
		return
	}
	if err != nil {
		writeAPIError(ctx, "INVALID_MENTION_TEXT")
		return
	}
	req.MentionTokens = mentionTokens
	// A new request consumes quota once. Idempotent replays returned above do
	// not consume another post_create allowance; failed creates are not refunded
	// in V1.
	if enabled && !ratelimit.Enforce(ctx, limiter, ratelimit.ActionPostCreate, ratelimit.FailClosed) {
		return
	}
	author, err := loadPostAuthorForCreate(ctx.Request.Context(), userID)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(ctx, "UNAUTHORIZED")
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	media, err := validatePostMediaRequests(ctx.Request.Context(), userID, req.Media)
	if err != nil {
		switch {
		default:
			writeAPIError(ctx, "INVALID_MEDIA")
		}
		return
	}

	var post models.Post
	err = persistPostGraphFn(ctx.Request.Context(), &post, userID, content, req, media, now)
	if err != nil {
		if errors.Is(err, errTooManyPostMentions) {
			writeAPIError(ctx, string(apierrors.CodePostMentionLimitExceeded))
			return
		}
		if hasClientPublishID && isClientPublishUniqueViolation(err) {
			existing, lookupErr := loadClientPublishPostFn(ctx.Request.Context(), userID, *clientPublishID)
			if lookupErr == nil {
				respondToExistingClientPublish(ctx, existing, clientPublishFingerprint, now)
				return
			}
			if handleRequestDBError(ctx, lookupErr) {
				return
			}
		}
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(ctx, "POST_REFERENCE_UNAVAILABLE")
			return
		}
		if errors.Is(err, errInvalidPostMedia) {
			writeAPIError(ctx, "INVALID_MEDIA")
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	initializePostLikeStateAfterCommit(ctx, post.ID)
	if post.ReplyToPostID != nil {
		if err := invalidatePostCreateParentDetailCache(*post.ReplyToPostID); err != nil {
			logAPIInternalError(ctx, http.StatusCreated, err)
		}
	}
	if post.QuotePostID != nil {
		if err := invalidatePostCreateParentDetailCache(*post.QuotePostID); err != nil {
			logAPIInternalError(ctx, http.StatusCreated, err)
		}
	}

	post.Author = models.User{
		Model:       gorm.Model{ID: author.ID},
		Username:    author.Username,
		DisplayName: author.DisplayName,
		AvatarURL:   author.AvatarURL,
	}
	response, err := buildPostCreateResponse(ctx.Request.Context(), post, postMediaResponsesFromValidated(media), now)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, response)
}

func persistPostGraph(ctx context.Context, post *models.Post, userID uint, content string, req createPostRequest, media []validatedPostMedia, now time.Time) error {
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	return global.APIDb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parentAuthor uint
		var quoteTargetAuthor uint
		*post = models.Post{
			AuthorID: userID, Content: content, Language: postlanguage.Detect(content), ReplyToPostID: req.ReplyToPostID,
			QuotePostID: req.QuotePostID, Visibility: "public",
			ClientPublishID: req.ClientPublishID, ClientPublishFingerprint: req.ClientPublishFingerprint,
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
			if err := publicPostScope(
				tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("posts.id = ?", *req.QuotePostID),
				now,
			).First(&quoted).Error; err != nil {
				return err
			}
			quoteTargetAuthor = quoted.AuthorID
		}
		post.CreatedAt = now
		post.UpdatedAt = now
		if err := tx.Create(post).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.PostLikeInitJob{
			PostID: post.ID, RetryAfter: now.UTC(), State: models.PostLikeInitJobPending,
		}).Error; err != nil {
			return err
		}
		if len(media) > 0 {
			mediaIDs := make([]string, len(media))
			for index, item := range media {
				mediaIDs[index] = item.MediaID
			}
			pendingUploads, err := lockPendingPostMediaUploads(tx, mediaIDs)
			if err != nil {
				return err
			}
			if len(pendingUploads) != len(media) {
				return errInvalidPostMedia
			}
			pendingByID := make(map[string]models.PostMediaUpload, len(pendingUploads))
			for _, upload := range pendingUploads {
				pendingByID[upload.MediaID] = upload
			}
			for position := range media {
				item := &media[position]
				upload, exists := pendingByID[item.MediaID]
				if !exists || upload.OwnerID != userID || upload.Status != postmediaupload.StatusUploaded || upload.MediumURL != item.PublicURL {
					return errInvalidPostMedia
				}
				item.LargeURL = upload.LargeURL
				item.Width = upload.Width
				item.Height = upload.Height
				if err := tx.Create(&models.PostMedia{
					PostID: post.ID, MediaType: item.MediaType, URL: item.PublicURL, LargeURL: item.LargeURL,
					Width: item.Width, Height: item.Height,
					Position: position, CreatedAt: now,
				}).Error; err != nil {
					return err
				}
			}
			if err := deleteConsumedPostMediaUploads(tx, mediaIDs, userID); err != nil {
				if errors.Is(err, postmediaupload.ErrUploadUnavailable) {
					return errInvalidPostMedia
				}
				return err
			}
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
			dirtyProfiles, err := recommendation.NewGormDirtyProfileRepository(tx)
			if err != nil {
				return err
			}
			if err := dirtyProfiles.InvalidateProfiles(ctx, []uint{userID}, "reply_created", now); err != nil {
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
		} else if post.QuotePostID != nil {
			rowsAffected, err := incrementPostQuoteCount(tx, *post.QuotePostID)
			if err != nil {
				return err
			}
			if rowsAffected != 1 {
				return errPostQuoteCountConsistency
			}
			activity, err := eventing.NewQuoteCreatedEnvelope(uuid.NewString(), eventing.QuoteCreatedPayload{
				QuotePostID: post.ID, TargetPostID: *post.QuotePostID,
				ActorID: userID, TargetAuthorID: quoteTargetAuthor, CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if err := addConfiguredActivityOutboxEvent(tx, activity); err != nil {
				return err
			}
		}
		if err := persistPostMentions(tx, post.ID, userID, req.MentionTokens, now, parentAuthor, quoteTargetAuthor); err != nil {
			return err
		}
		if config.AppConfig != nil && config.AppConfig.Embedding.Enabled {
			embeddingEvent, err := eventing.NewPostEmbeddingRequestedEnvelope(uuid.NewString(), post.ID, now)
			if err != nil {
				return err
			}
			if err := addConfiguredPostEmbeddingOutboxEvent(tx, embeddingEvent); err != nil {
				return err
			}
		}
		return nil
	})
}

func persistPostMentions(tx *gorm.DB, postID, actorID uint, tokens []string, createdAt time.Time, replyAuthorID, quoteAuthorID uint) error {
	if len(tokens) == 0 {
		return nil
	}
	var users []models.User
	if err := tx.Select("id", "username").Where("username IN ? AND deleted_at IS NULL", tokens).Find(&users).Error; err != nil {
		return err
	}
	byUsername := make(map[string]uint, len(users))
	for _, user := range users {
		byUsername[user.Username] = user.ID
	}
	recipients := make([]uint, 0, len(users))
	seen := make(map[uint]struct{}, len(users))
	for _, token := range tokens {
		id := byUsername[token]
		if id == 0 || id == actorID {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		recipients = append(recipients, id)
	}
	if len(recipients) > 5 {
		return errTooManyPostMentions
	}
	if len(recipients) == 0 {
		return nil
	}
	if err := upsertUserMentionRecents(tx, actorID, recipients, createdAt); err != nil {
		return err
	}
	for _, recipientID := range recipients {
		if recipientID == replyAuthorID || recipientID == quoteAuthorID {
			continue
		}
		envelope, err := eventing.NewPostMentionedEnvelope(uuid.NewString(), eventing.PostMentionedPayload{
			PostID: postID, ActorID: actorID, RecipientID: recipientID, CreatedAt: createdAt,
		})
		if err != nil {
			return err
		}
		if err := addConfiguredActivityOutboxEvent(tx, envelope); err != nil {
			return err
		}
	}
	return nil
}

func upsertUserMentionRecents(tx *gorm.DB, actorID uint, recipientIDs []uint, mentionedAt time.Time) error {
	if len(recipientIDs) == 0 {
		return nil
	}
	recents := make([]models.UserMentionRecent, 0, len(recipientIDs))
	for _, recipientID := range recipientIDs {
		recents = append(recents, models.UserMentionRecent{UserID: actorID, MentionedUserID: recipientID, LastMentionedAt: mentionedAt.UTC()})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "mentioned_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"last_mentioned_at": gorm.Expr("GREATEST(user_mention_recents.last_mentioned_at, EXCLUDED.last_mentioned_at)"),
		}),
	}).Create(&recents).Error
}

func GetPostByID(ctx *gin.Context) {
	id := ctx.Param("id")
	post, err := loadPostDetail(ctx.Request.Context(), id)
	if err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(ctx, "NOT_FOUND")
			return
		}
		writeInternalAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, post)
}
