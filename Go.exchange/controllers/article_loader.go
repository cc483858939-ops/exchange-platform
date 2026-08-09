package controllers

import (
	"errors"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const articleListSelectColumns = "id,title,preview,cover_image_url,expired_at,created_at,updated_at,deleted_at,author_id,like_count,comment_count"

func visibleArticleScope(query *gorm.DB, now time.Time) *gorm.DB {
	return query.Where("expired_at > ? OR expired_at IS NULL", now)
}

func loadArticleList() ([]articleResponse, error) {
	return loadJSONCache(articleListCacheKey, func() ([]articleResponse, error) {
		if global.Db == nil {
			return nil, errors.New("database is not initialized")
		}
		query := global.Db.
			Select(articleListSelectColumns).
			Scopes(func(tx *gorm.DB) *gorm.DB { return visibleArticleScope(tx, time.Now()) }).
			Order("created_at DESC, id DESC")
		return loadArticleResponses(query)
	})
}

func loadArticleDetail(id string) (articleResponse, error) {
	return loadJSONCache(articleDetailCacheKey(id), func() (articleResponse, error) {
		if global.Db == nil {
			return articleResponse{}, errors.New("database is not initialized")
		}
		var article models.Article
		err := preloadArticleAuthor(global.Db).
			Where("id = ? AND (expired_at > ? OR expired_at IS NULL)", id, time.Now()).
			First(&article).Error
		if err != nil {
			return articleResponse{}, err
		}
		return newArticleResponse(article)
	})
}
