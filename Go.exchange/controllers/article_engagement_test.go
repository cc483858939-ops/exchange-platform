package controllers

import (
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestArticleResponseIncludesEngagementMetadata(t *testing.T) {
	article := models.Article{
		Model:        gorm.Model{ID: 12, CreatedAt: time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)},
		AuthorID:     7,
		Author:       models.User{Model: gorm.Model{ID: 7}, Username: "alice"},
		Title:        "engagement",
		Preview:      "engagement",
		LikeCount:    17,
		CommentCount: 8,
	}

	response, err := newArticleResponse(article)
	if err != nil {
		t.Fatal(err)
	}
	if response.LikeCount != 17 || response.CommentCount != 8 {
		t.Fatalf("response like_count=%d comment_count=%d", response.LikeCount, response.CommentCount)
	}
}

func TestArticleEngagementCacheSchemaVersion(t *testing.T) {
	if articleListCacheKey != "articles:v3" {
		t.Fatalf("article list cache key=%q", articleListCacheKey)
	}
	if articleDetailCacheKey("42") != "article:detail:v3:42" {
		t.Fatalf("article detail cache key=%q", articleDetailCacheKey("42"))
	}
	if !strings.Contains(articleListSelectColumns, "like_count") || !strings.Contains(articleListSelectColumns, "comment_count") {
		t.Fatalf("article list select columns=%q", articleListSelectColumns)
	}
}

func TestArticleListSelectionIncludesEngagementMetadataIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	if err := db.Model(&fixture.Article).Updates(map[string]any{
		"like_count":    17,
		"comment_count": 8,
	}).Error; err != nil {
		t.Fatal(err)
	}

	responses, err := loadArticleResponses(
		db.Select(articleListSelectColumns).
			Where("id = ?", fixture.Article.ID).
			Scopes(func(tx *gorm.DB) *gorm.DB { return visibleArticleScope(tx, time.Now()) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || responses[0].LikeCount != 17 || responses[0].CommentCount != 8 {
		t.Fatalf("article list responses=%#v", responses)
	}
}
