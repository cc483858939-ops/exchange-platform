package controllers

import (
	"errors"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const publicArticleSelectColumns = "articles.id,articles.created_at,articles.updated_at,articles.author_id,articles.title,articles.content,articles.preview,articles.cover_image_url,articles.summary,articles.tags,articles.category,articles.publication_state,articles.analysis_state,articles.analysis_version,articles.published_at,articles.expired_at,articles.like_count,articles.comment_count,articles.like_sync_version"

func publicArticleScope(query *gorm.DB, now time.Time) *gorm.DB {
	now = now.UTC()
	return query.
		Where("articles.deleted_at IS NULL").
		Where("articles.publication_state = ?", consts.ArticlePublicationStatePublished).
		Where("articles.published_at IS NOT NULL").
		Where("articles.published_at <= ?", now).
		Where("(articles.expired_at IS NULL OR articles.expired_at > ?)", now)
}

var invalidateArticleDetailCacheKey = func(key string) error {
	if global.RedisDB == nil {
		return nil
	}
	return global.RedisDB.Del(key).Err()
}

func isPublicArticleResponseAt(article articleResponse, now time.Time) bool {
	if article.PublicationState != consts.ArticlePublicationStatePublished || article.PublishedAt == nil {
		return false
	}
	now = now.UTC()
	if article.PublishedAt.After(now) {
		return false
	}
	return article.ExpiredAt == nil || article.ExpiredAt.After(now)
}

var loadArticleDetailCache = func(key string, loader func() (articleResponse, error)) (articleResponse, error) {
	return loadJSONCache(key, loader)
}

func loadArticleDetail(id string) (articleResponse, error) {
	key := articleDetailCacheKey(id)
	response, err := loadArticleDetailCache(key, func() (articleResponse, error) {
		if global.Db == nil {
			return articleResponse{}, errors.New("database is not initialized")
		}
		now := time.Now().UTC()
		var article models.Article
		err := publicArticleScope(preloadArticleAuthor(global.Db).Where("articles.id = ?", id), now).
			First(&article).Error
		if err != nil {
			return articleResponse{}, err
		}
		return newArticleResponse(article)
	})
	if err != nil {
		return articleResponse{}, err
	}
	if isPublicArticleResponseAt(response, time.Now().UTC()) {
		return response, nil
	}
	_ = invalidateArticleDetailCacheKey(key)
	return articleResponse{}, gorm.ErrRecordNotFound
}
