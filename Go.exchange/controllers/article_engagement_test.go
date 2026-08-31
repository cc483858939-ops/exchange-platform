package controllers

import (
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestPostResponseIncludesEngagementMetadata(t *testing.T) {
	article := models.Post{
		Model:      gorm.Model{ID: 12, CreatedAt: time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)},
		AuthorID:   7,
		Author:     models.User{Model: gorm.Model{ID: 7}, Username: "alice"},
		Content:    "engagement",
		Visibility: "public",
		LikeCount:  17,
		ReplyCount: 8,
		ViewCount:  1234,
	}

	response, err := newPostResponse(article)
	if err != nil {
		t.Fatal(err)
	}
	if response.LikeCount != 17 || response.ReplyCount != 8 || response.ViewCount != 1234 {
		t.Fatalf("response like_count=%d comment_count=%d view_count=%d", response.LikeCount, response.ReplyCount, response.ViewCount)
	}
}

func TestPostEngagementCacheSchemaVersion(t *testing.T) {
	if postDetailCacheKey("42") != "post:detail:v1:42" {
		t.Fatalf("article detail cache key=%q", postDetailCacheKey("42"))
	}
	for _, column := range []string{
		"id", "created_at", "updated_at", "author_id", "content", "reply_to_post_id",
		"quote_post_id", "conversation_id", "visibility", "like_count", "reply_count", "view_count", "like_sync_version",
	} {
		if !strings.Contains(publicPostSelectColumns, "posts."+column) {
			t.Fatalf("public article select columns missing posts.%s: %q", column, publicPostSelectColumns)
		}
	}
}
func TestPublicPostSelectionIncludesEngagementMetadataIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	if err := db.Model(&fixture.Article).Updates(map[string]any{
		"like_count":  17,
		"reply_count": 8,
		"view_count":  4321,
	}).Error; err != nil {
		t.Fatal(err)
	}

	responses, err := loadPostResponses(
		db.Select(publicPostSelectColumns).
			Where("id = ?", fixture.Article.ID).
			Scopes(func(tx *gorm.DB) *gorm.DB { return publicPostScope(tx, time.Now().UTC()) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || responses[0].LikeCount != 17 || responses[0].ReplyCount != 8 || responses[0].ViewCount != 4321 {
		t.Fatalf("article list responses=%#v", responses)
	}
}
