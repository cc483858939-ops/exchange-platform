package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const publicPostSelectColumns = "posts.id,posts.created_at,posts.updated_at,posts.author_id,posts.content,posts.language,posts.reply_to_post_id,posts.quote_post_id,posts.conversation_id,posts.visibility,posts.like_count,posts.reply_count,posts.view_count,posts.like_sync_version,posts.deleted_at"

// publicPostEligibilitySQL is the single SQL contract used by raw timeline
// queries. publicPostScope below exposes the same predicates to GORM queries.
func publicPostEligibilitySQL(postAlias string) string {
	return fmt.Sprintf(`
%s.deleted_at IS NULL
AND %s.visibility = 'public'
AND EXISTS (
    SELECT 1 FROM users AS post_author
    WHERE post_author.id = %s.author_id
      AND post_author.deleted_at IS NULL
)
`, postAlias, postAlias, postAlias)
}

// publicPostScope is shared by detail, profile, feed, history,
// recommendations, and notifications.
func publicPostScope(query *gorm.DB, now time.Time) *gorm.DB {
	return query.Where(publicPostEligibilitySQL("posts"))
}

var invalidatePostDetailCacheKey = func(key string) error {
	if global.RedisDB == nil {
		return nil
	}
	return global.RedisDB.Del(key).Err()
}

func isPublicPostResponseAt(post postResponse, now time.Time) bool {
	if post.Deleted || post.Visibility != "public" || post.PublishedAt == nil {
		return false
	}
	return true
}

var loadPostDetailCache = func(ctx context.Context, key string, loader func() (postResponse, error)) (postResponse, error) {
	return loadJSONCacheWithContext(ctx, key, loader)
}

func loadPostDetail(ctx context.Context, id string) (postResponse, error) {
	db := global.Db
	if db != nil {
		db = db.WithContext(ctx)
	}
	key := postDetailCacheKey(id)
	loader := func() (postResponse, error) {
		if db == nil {
			return postResponse{}, errors.New("database is not initialized")
		}
		post, err := loadPublicPost(db, id, time.Now().UTC())
		if err != nil {
			return postResponse{}, err
		}
		response, err := postResponseFromModel(post)
		if err != nil {
			return postResponse{}, err
		}
		if err := hydratePostResponseMediaFromDB(db, &response); err != nil {
			return postResponse{}, err
		}
		return response, nil
	}

	response, err := loadPostDetailCache(ctx, key, loader)
	if err != nil {
		return postResponse{}, err
	}
	ensurePostResponseMedia(&response)
	if db != nil {
		responses := []postResponse{response}
		if err := hydratePostResponseRepostCountsFromDB(db, responses); err != nil {
			return postResponse{}, err
		}
		response = responses[0]
	}
	// Reference fields are always loaded after the viewer-independent base
	// record, so a deleted target becomes a tombstone before the response.
	if err := hydratePostResponseReferencesFromDB(db, &response, time.Now().UTC()); err != nil {
		return postResponse{}, err
	}
	if isPublicPostResponseAt(response, time.Now().UTC()) {
		return response, nil
	}
	_ = invalidatePostDetailCacheKey(key)
	return postResponse{}, gorm.ErrRecordNotFound
}

func loadPublicPost(db *gorm.DB, rawID string, now time.Time) (models.Post, error) {
	id, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return models.Post{}, gorm.ErrRecordNotFound
	}
	var post models.Post
	if err := publicPostScope(preloadPostAuthor(db.Model(&models.Post{})).Where("posts.id = ?", uint(id)), now).First(&post).Error; err != nil {
		return models.Post{}, err
	}
	return post, nil
}

func hydratePostResponseReferencesFromDB(db *gorm.DB, response *postResponse, now time.Time) error {
	if response == nil {
		return nil
	}
	responses := []postResponse{*response}
	if err := hydratePostResponsesReferencesFromDB(db, responses, now); err != nil {
		return err
	}
	*response = responses[0]
	return nil
}

func hydratePostResponsesReferencesFromDB(db *gorm.DB, responses []postResponse, now time.Time) error {
	if len(responses) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(responses)*2)
	for index := range responses {
		if responses[index].ReplyToPostID != nil {
			ids = append(ids, *responses[index].ReplyToPostID)
		}
		if responses[index].QuotePostID != nil {
			ids = append(ids, *responses[index].QuotePostID)
		}
	}
	references, err := loadPostReferencesByIDsFromDB(db, ids, now)
	if err != nil {
		return err
	}
	for index := range responses {
		responses[index].ReplyToPost = copyPostReference(references, responses[index].ReplyToPostID)
		responses[index].QuotePost = copyPostReference(references, responses[index].QuotePostID)
	}
	return nil
}

func copyPostReference(references map[uint]postReferenceResponse, id *uint) *postReferenceResponse {
	if id == nil || *id == 0 {
		return nil
	}
	reference, ok := references[*id]
	if !ok {
		return nil
	}
	copy := reference
	if reference.Author != nil {
		author := *reference.Author
		copy.Author = &author
	}
	if reference.PublishedAt != nil {
		publishedAt := *reference.PublishedAt
		copy.PublishedAt = &publishedAt
	}
	if reference.Media != nil {
		copy.Media = append(make([]postMediaResponse, 0, len(reference.Media)), reference.Media...)
	}
	return &copy
}

func loadPostReferencesByIDsFromDB(db *gorm.DB, ids []uint, now time.Time) (map[uint]postReferenceResponse, error) {
	uniqueIDs := make([]uint, 0, len(ids))
	references := make(map[uint]postReferenceResponse, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
		references[id] = postReferenceResponse{ID: id, Deleted: true}
	}
	if len(uniqueIDs) == 0 {
		return references, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}

	var posts []models.Post
	query := preloadPostAuthor(db.Model(&models.Post{})).
		Select(publicPostSelectColumns).
		Where("posts.id IN ?", uniqueIDs)
	if err := publicPostScope(query, now).Find(&posts).Error; err != nil {
		return nil, err
	}
	activeIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		author, err := publicAuthorFromPost(post)
		if err != nil {
			return nil, err
		}
		publishedAt := post.CreatedAt.UTC()
		references[post.ID] = postReferenceResponse{
			ID: post.ID, Deleted: false, Author: &author, Content: post.Content,
			PublishedAt: &publishedAt,
		}
		activeIDs = append(activeIDs, post.ID)
	}
	if len(activeIDs) == 0 {
		return references, nil
	}
	mediaByPostID, err := loadPostMediaByPostIDs(db, activeIDs)
	if err != nil {
		return nil, err
	}
	for _, postID := range activeIDs {
		reference := references[postID]
		reference.Media = mediaByPostID[postID]
		if reference.Media == nil {
			reference.Media = make([]postMediaResponse, 0)
		}
		references[postID] = reference
	}
	return references, nil
}

func loadPostReferenceFromDB(db *gorm.DB, id *uint, now time.Time) (*postReferenceResponse, error) {
	if id == nil || *id == 0 {
		return nil, nil
	}
	references, err := loadPostReferencesByIDsFromDB(db, []uint{*id}, now)
	if err != nil {
		return nil, err
	}
	return copyPostReference(references, id), nil
}
