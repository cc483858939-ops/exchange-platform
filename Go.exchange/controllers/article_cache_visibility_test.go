package controllers

import (
	"errors"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"

	"gorm.io/gorm"
)

func TestLoadArticleDetailRejectsEveryNonPublicCachedResponse(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	expired := now.Add(-time.Minute)
	cases := []struct {
		name     string
		response articleResponse
	}{
		{
			name: "expired",
			response: articleResponse{
				ID:               42,
				PublicationState: consts.ArticlePublicationStatePublished,
				PublishedAt:      &past,
				ExpiredAt:        &expired,
			},
		},
		{
			name: "future",
			response: articleResponse{
				ID:               42,
				PublicationState: consts.ArticlePublicationStatePublished,
				PublishedAt:      &future,
			},
		},
		{
			name: "missing published at",
			response: articleResponse{
				ID:               42,
				PublicationState: consts.ArticlePublicationStatePublished,
			},
		},
		{
			name: "unpublished",
			response: articleResponse{
				ID:               42,
				PublicationState: "draft",
				PublishedAt:      &past,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			originalCacheLoader := loadArticleDetailCache
			originalInvalidator := invalidateArticleDetailCacheKey
			originalDB := global.Db
			t.Cleanup(func() {
				loadArticleDetailCache = originalCacheLoader
				invalidateArticleDetailCacheKey = originalInvalidator
				global.Db = originalDB
			})

			global.Db = nil
			loadArticleDetailCache = func(string, func() (articleResponse, error)) (articleResponse, error) {
				return testCase.response, nil
			}
			var deletedKey string
			invalidateArticleDetailCacheKey = func(key string) error {
				deletedKey = key
				return errors.New("redis unavailable")
			}

			_, err := loadArticleDetail("42")
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("error=%v want=%v", err, gorm.ErrRecordNotFound)
			}
			if deletedKey != articleDetailCacheKey("42") {
				t.Fatalf("deleted key=%q", deletedKey)
			}
		})
	}
}
