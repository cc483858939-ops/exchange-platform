package controllers

import (
	"errors"
	"testing"
	"time"

	"Go.exchange/global"

	"gorm.io/gorm"
)

func TestLoadPostDetailRejectsEveryNonPublicCachedResponse(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	cases := []struct {
		name     string
		response postResponse
	}{
		{
			name: "deleted",
			response: postResponse{
				ID: 42, PublishedAt: &past, Visibility: "public", Deleted: true,
			},
		},
		{
			name: "private",
			response: postResponse{
				ID: 42, PublishedAt: &past, Visibility: "private",
			},
		},
		{
			name: "missing published at",
			response: postResponse{
				ID: 42,
			},
		},
		{
			name: "public missing timestamp",
			response: postResponse{
				ID: 42, Visibility: "public",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			originalCacheLoader := loadPostDetailCache
			originalInvalidator := invalidatePostDetailCacheKey
			originalDB := global.Db
			t.Cleanup(func() {
				loadPostDetailCache = originalCacheLoader
				invalidatePostDetailCacheKey = originalInvalidator
				global.Db = originalDB
			})

			global.Db = nil
			loadPostDetailCache = func(string, func() (postResponse, error)) (postResponse, error) {
				return testCase.response, nil
			}
			var deletedKey string
			invalidatePostDetailCacheKey = func(key string) error {
				deletedKey = key
				return errors.New("redis unavailable")
			}

			_, err := loadPostDetail("42")
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("error=%v want=%v", err, gorm.ErrRecordNotFound)
			}
			if deletedKey != postDetailCacheKey("42") {
				t.Fatalf("deleted key=%q", deletedKey)
			}
		})
	}
}
