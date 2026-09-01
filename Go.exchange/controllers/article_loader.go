package controllers

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const publicPostSelectColumns = "posts.id,posts.created_at,posts.updated_at,posts.author_id,posts.content,posts.reply_to_post_id,posts.quote_post_id,posts.conversation_id,posts.visibility,posts.like_count,posts.reply_count,posts.view_count,posts.like_sync_version,posts.deleted_at"

// publicPostEligibilitySQL is the single SQL contract used by raw timeline
// queries. publicPostScope below exposes the same predicates to GORM queries.
func publicPostEligibilitySQL(postAlias, articleAlias string) string {
	return fmt.Sprintf(`
%s.deleted_at IS NULL
AND %s.visibility = 'public'
AND EXISTS (
    SELECT 1 FROM users AS post_author
    WHERE post_author.id = %s.author_id
      AND post_author.deleted_at IS NULL
)
AND (
    NOT EXISTS (
        SELECT 1 FROM post_articles AS pa_any
        WHERE pa_any.post_id = %s.id
    )
    OR EXISTS (
        SELECT 1 FROM post_articles AS pa_valid
        WHERE pa_valid.post_id = %s.id
          AND pa_valid.publication_state = 'published'
          AND pa_valid.published_at IS NOT NULL
          AND pa_valid.published_at <= ?
          AND (pa_valid.expired_at IS NULL OR pa_valid.expired_at > ?)
    )
)`, postAlias, postAlias, postAlias, postAlias, postAlias)
}

// publicPostScope is shared by detail, profile, feed, history,
// recommendations, and notifications. An existing malformed PostArticle row
// is not allowed to fall back to normal-Post eligibility.
func publicPostScope(query *gorm.DB, now time.Time) *gorm.DB {
	return query.Where(publicPostEligibilitySQL("posts", "pa"), now.UTC(), now.UTC())
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
	now = now.UTC()
	if post.PublishedAt.After(now) {
		return false
	}
	if post.Article == nil {
		return true
	}
	return post.Article.PublicationState == "published" &&
		post.Article.PublishedAt != nil && !post.Article.PublishedAt.After(now) &&
		(post.Article.ExpiredAt == nil || post.Article.ExpiredAt.After(now))
}

var loadPostDetailCache = func(key string, loader func() (postResponse, error)) (postResponse, error) {
	return loadJSONCache(key, loader)
}

func loadPostDetail(id string) (postResponse, error) {
	key := postDetailCacheKey(id)
	loader := func() (postResponse, error) {
		if global.Db == nil {
			return postResponse{}, errors.New("database is not initialized")
		}
		post, article, err := loadPublicPostWithArticle(global.Db, id, time.Now().UTC())
		if err != nil {
			return postResponse{}, err
		}
		return postResponseFromModel(post, article)
	}

	response, err := loadPostDetailCache(key, loader)
	if err != nil {
		return postResponse{}, err
	}
	// Reference fields are always loaded after the viewer-independent base
	// record, so a deleted target becomes a tombstone before the response.
	if err := hydratePostResponseReferences(&response, time.Now().UTC()); err != nil {
		return postResponse{}, err
	}
	if isPublicPostResponseAt(response, time.Now().UTC()) {
		return response, nil
	}
	_ = invalidatePostDetailCacheKey(key)
	return postResponse{}, gorm.ErrRecordNotFound
}

func effectivePublishedAtSQL(postAlias, articleAlias string) string {
	return fmt.Sprintf("COALESCE(%s.published_at, %s.created_at)", articleAlias, postAlias)
}

func loadPostArticlesFromDB(db *gorm.DB, posts []models.Post) (map[uint]*models.PostArticle, error) {
	result := make(map[uint]*models.PostArticle, len(posts))
	if len(posts) == 0 {
		return result, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	ids := make([]uint, 0, len(posts))
	for _, post := range posts {
		ids = append(ids, post.ID)
	}
	var articles []models.PostArticle
	if err := db.Where("post_id IN ?", ids).Find(&articles).Error; err != nil {
		return nil, err
	}
	for index := range articles {
		article := articles[index]
		result[article.PostID] = &article
	}
	return result, nil
}

func loadPostArticles(posts []models.Post) (map[uint]*models.PostArticle, error) {
	return loadPostArticlesFromDB(global.Db, posts)
}

func loadPublicPostWithArticle(db *gorm.DB, rawID string, now time.Time) (models.Post, *models.PostArticle, error) {
	id, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return models.Post{}, nil, gorm.ErrRecordNotFound
	}
	var post models.Post
	if err := publicPostScope(preloadPostAuthor(db.Model(&models.Post{})).Where("posts.id = ?", uint(id)), now).First(&post).Error; err != nil {
		return models.Post{}, nil, err
	}
	articles, err := loadPostArticlesFromDB(db, []models.Post{post})
	if err != nil {
		return models.Post{}, nil, err
	}
	article := articles[post.ID]
	if article == nil {
		return post, nil, nil
	}
	return post, article, nil
}

func hydratePostResponseReferences(response *postResponse, now time.Time) error {
	return hydratePostResponseReferencesFromDB(global.Db, response, now)
}

func hydratePostResponseReferencesFromDB(db *gorm.DB, response *postResponse, now time.Time) error {
	if response == nil {
		return nil
	}
	if response.ReplyToPostID != nil {
		reference, err := loadPostReferenceFromDB(db, response.ReplyToPostID, now)
		if err != nil {
			return err
		}
		response.ReplyToPost = reference
	} else {
		response.ReplyToPost = nil
	}
	if response.QuotePostID != nil {
		reference, err := loadPostReferenceFromDB(db, response.QuotePostID, now)
		if err != nil {
			return err
		}
		response.QuotePost = reference
	} else {
		response.QuotePost = nil
	}
	return nil
}

func loadPostReference(id *uint, now time.Time) (*postReferenceResponse, error) {
	return loadPostReferenceFromDB(global.Db, id, now)
}

func loadPostReferenceFromDB(db *gorm.DB, id *uint, now time.Time) (*postReferenceResponse, error) {
	if id == nil || *id == 0 {
		return nil, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}

	var structuralPost models.Post
	if err := db.Unscoped().Model(&models.Post{}).Where("posts.id = ?", *id).First(&structuralPost).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &postReferenceResponse{ID: *id, State: postReferenceStateUnavailable}, nil
		}
		return nil, err
	}
	if structuralPost.DeletedAt.Valid {
		return &postReferenceResponse{ID: *id, State: postReferenceStateDeleted}, nil
	}

	var post models.Post
	err := publicPostScope(preloadPostAuthor(db.Model(&models.Post{})).Where("posts.id = ?", *id), now).First(&post).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &postReferenceResponse{ID: *id, State: postReferenceStateUnavailable}, nil
		}
		return nil, err
	}
	articles, err := loadPostArticlesFromDB(db, []models.Post{post})
	if err != nil {
		return nil, err
	}
	article := articles[post.ID]
	publishedAt := post.CreatedAt.UTC()
	if article != nil && article.PublishedAt != nil {
		publishedAt = article.PublishedAt.UTC()
	}
	author, err := publicAuthorFromPost(post)
	if err != nil {
		return nil, err
	}
	return &postReferenceResponse{
		ID: post.ID, State: postReferenceStateActive, Author: &author, Content: post.Content,
		PublishedAt: &publishedAt, Article: postArticleResponseFromModel(article),
	}, nil
}
